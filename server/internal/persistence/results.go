package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/migrations"
)

const resultSchemaVersion = 1

var (
	ErrInvalidResultEnvelope = errors.New("invalid result envelope")
	ErrResultConflict        = errors.New("match ID already contains a different result")
	ErrResultNotFound        = errors.New("match result not found")
	ErrResultBackend         = errors.New("match result backend unavailable")
	ErrInvalidResultStore    = errors.New("invalid result store options")
	matchIDPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// ResultEnvelope adds D's stable cross-retry identity and Room correlation to
// B's detached GameResult. MatchID must be generated before Room teardown and
// reused unchanged for every delivery retry.
type ResultEnvelope struct {
	MatchID   string          `json:"match_id"`
	RoomID    uint64          `json:"room_id"`
	CreatedAt time.Time       `json:"created_at"`
	Result    game.GameResult `json:"result"`
}

func (e ResultEnvelope) Clone() ResultEnvelope {
	e.Result = e.Result.Clone()
	return e
}

func (e ResultEnvelope) Validate() error {
	if !matchIDPattern.MatchString(e.MatchID) || e.RoomID == 0 || !e.Result.Outcome.Valid() ||
		e.Result.EndedAtTick < e.Result.StartedAtTick || e.Result.FinalStageIndex == 0 || len(e.Result.Players) == 0 || len(e.Result.Players) > 128 {
		return ErrInvalidResultEnvelope
	}
	previousStage := uint32(0)
	for _, summary := range e.Result.ClearedStages {
		if summary.Index == 0 || summary.Index <= previousStage || summary.Index > e.Result.FinalStageIndex ||
			summary.ClearTick < e.Result.StartedAtTick || summary.ClearTick > e.Result.EndedAtTick ||
			math.IsNaN(summary.DifficultyScore) || math.IsInf(summary.DifficultyScore, 0) || summary.DifficultyScore <= 0 ||
			summary.Performance.Validate() != nil {
			return ErrInvalidResultEnvelope
		}
		previousStage = summary.Index
	}
	if e.Result.Outcome == game.GameVictory && len(e.Result.ClearedStages) == 0 {
		return ErrInvalidResultEnvelope
	}
	players := make(map[uint64]struct{}, len(e.Result.Players))
	for _, player := range e.Result.Players {
		id := uint64(player.PlayerID)
		if id == 0 || !player.Stats.Valid() || math.IsNaN(player.Health) || math.IsInf(player.Health, 0) ||
			player.Health < 0 || player.Health > player.Stats.MaxHealth || (player.Alive && player.Health <= 0) {
			return ErrInvalidResultEnvelope
		}
		if _, exists := players[id]; exists {
			return ErrInvalidResultEnvelope
		}
		players[id] = struct{}{}
	}
	return nil
}

type PersistDisposition uint8

const (
	PersistInserted PersistDisposition = iota + 1
	PersistIdempotent
)

type ResultStoreOptions struct {
	DSN              string
	OperationTimeout time.Duration
	ApplyMigrations  bool
}

func (o ResultStoreOptions) Validate() error {
	if o.DSN == "" || o.OperationTimeout <= 0 {
		return ErrInvalidResultStore
	}
	if _, err := mysql.ParseDSN(o.DSN); err != nil {
		return fmt.Errorf("%w: malformed DSN", ErrInvalidResultStore)
	}
	return nil
}

type ResultStore struct {
	database         *sql.DB
	operationTimeout time.Duration
}

func OpenResultStore(ctx context.Context, options ResultStoreOptions) (*ResultStore, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	database, err := sql.Open("mysql", options.DSN)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", ErrResultBackend, err)
	}
	database.SetMaxOpenConns(8)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(5 * time.Minute)
	operationContext, cancel := context.WithTimeout(ctx, options.OperationTimeout)
	defer cancel()
	if err := database.PingContext(operationContext); err != nil {
		_ = database.Close()
		return nil, resultBackendError("startup ping", err)
	}
	if options.ApplyMigrations {
		if err := migrations.Apply(operationContext, database); err != nil {
			_ = database.Close()
			return nil, resultBackendError("migrations", err)
		}
	}
	return &ResultStore{database: database, operationTimeout: options.OperationTimeout}, nil
}

func (s *ResultStore) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	return s.database.Close()
}

func (s *ResultStore) Persist(ctx context.Context, envelope ResultEnvelope) (PersistDisposition, error) {
	if err := envelope.Validate(); err != nil {
		return 0, err
	}
	payload, hash, err := resultPayload(envelope)
	if err != nil {
		return 0, err
	}
	createdAt := envelope.CreatedAt.UTC()
	if envelope.CreatedAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	operationContext, cancel := withOperationTimeout(ctx, s.operationTimeout)
	defer cancel()
	tx, err := s.database.BeginTx(operationContext, nil)
	if err != nil {
		return 0, resultBackendError("begin", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(operationContext, `INSERT INTO match_results
        (match_id, room_id, outcome, started_tick, ended_tick, final_stage_index, result_json, payload_sha256, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		envelope.MatchID, envelope.RoomID, envelope.Result.Outcome.String(), envelope.Result.StartedAtTick,
		envelope.Result.EndedAtTick, envelope.Result.FinalStageIndex, payload, hash[:], createdAt)
	if err != nil {
		var mysqlError *mysql.MySQLError
		if !errors.As(err, &mysqlError) || mysqlError.Number != 1062 {
			return 0, resultBackendError("insert result", err)
		}
		var existing []byte
		if err := tx.QueryRowContext(operationContext, "SELECT payload_sha256 FROM match_results WHERE match_id = ? FOR UPDATE", envelope.MatchID).Scan(&existing); err != nil {
			return 0, resultBackendError("verify duplicate", err)
		}
		if !bytes.Equal(existing, hash[:]) {
			return 0, ErrResultConflict
		}
		if err := tx.Commit(); err != nil {
			return 0, resultBackendError("commit duplicate", err)
		}
		return PersistIdempotent, nil
	}
	for _, player := range envelope.Result.Players {
		_, err := tx.ExecContext(operationContext, `INSERT INTO match_players
            (match_id, player_id, alive, health, attack_value, defense_value, max_health, move_speed,
             attack_cooldown_ticks, weapon_id, relic_id, potion_id)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, envelope.MatchID, uint64(player.PlayerID), player.Alive,
			player.Health, player.Stats.Attack, player.Stats.Defense, player.Stats.MaxHealth, player.Stats.MoveSpeed,
			player.Stats.AttackCooldownTicks, player.Equipment.WeaponID, player.Equipment.RelicID, player.Equipment.PotionID)
		if err != nil {
			return 0, resultBackendError("insert player result", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, resultBackendError("commit result", err)
	}
	return PersistInserted, nil
}

func (s *ResultStore) Load(ctx context.Context, matchID string) (ResultEnvelope, error) {
	if !matchIDPattern.MatchString(matchID) {
		return ResultEnvelope{}, ErrInvalidResultEnvelope
	}
	operationContext, cancel := withOperationTimeout(ctx, s.operationTimeout)
	defer cancel()
	var envelope ResultEnvelope
	var payload []byte
	envelope.MatchID = matchID
	err := s.database.QueryRowContext(operationContext,
		"SELECT room_id, created_at, result_json FROM match_results WHERE match_id = ?", matchID).
		Scan(&envelope.RoomID, &envelope.CreatedAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ResultEnvelope{}, ErrResultNotFound
	}
	if err != nil {
		return ResultEnvelope{}, resultBackendError("load", err)
	}
	if err := json.Unmarshal(payload, &envelope.Result); err != nil {
		return ResultEnvelope{}, fmt.Errorf("%w: stored JSON is corrupt", ErrResultBackend)
	}
	if err := envelope.Validate(); err != nil {
		return ResultEnvelope{}, fmt.Errorf("%w: stored result is invalid", ErrResultBackend)
	}
	return envelope, nil
}

func resultPayload(envelope ResultEnvelope) ([]byte, [sha256.Size]byte, error) {
	canonical := struct {
		SchemaVersion int             `json:"schema_version"`
		RoomID        uint64          `json:"room_id"`
		Result        game.GameResult `json:"result"`
	}{resultSchemaVersion, envelope.RoomID, envelope.Result}
	payload, err := json.Marshal(canonical.Result)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("encode result: %w", err)
	}
	hashPayload, err := json.Marshal(canonical)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("hash result: %w", err)
	}
	return payload, sha256.Sum256(hashPayload), nil
}

func withOperationTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, set := ctx.Deadline(); set {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func resultBackendError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrResultBackend, operation, err)
}
