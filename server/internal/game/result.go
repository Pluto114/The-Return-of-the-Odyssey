package game

import (
	"errors"
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

type GameOutcome uint8

const (
	GameVictory GameOutcome = iota + 1
	GameDefeat
	GameAbandoned
)

var (
	ErrInvalidGameOutcome = errors.New("invalid game outcome")
	ErrGameResultState    = errors.New("game result is not available in the current state")
)

func (o GameOutcome) Valid() bool {
	return o == GameVictory || o == GameDefeat || o == GameAbandoned
}

func (o GameOutcome) String() string {
	switch o {
	case GameVictory:
		return "victory"
	case GameDefeat:
		return "defeat"
	case GameAbandoned:
		return "abandoned"
	default:
		return "unknown"
	}
}

// StageSummary is the persistence-sized history of one cleared stage. The
// full current Plan remains available through CompletedStage for Director use.
type StageSummary struct {
	Index           uint32
	Seed            int64
	DifficultyScore float64
	ClearTick       uint64
	Performance     director.PerformanceMetrics
}

type PlayerResult struct {
	PlayerID  entity.ID
	Alive     bool
	Health    float64
	Stats     entity.CombatStats
	Equipment entity.EquipmentState
}

// GameResult is a detached, protocol-independent value for D's asynchronous
// persistence. Generating it never changes World or performs I/O.
type GameResult struct {
	Outcome         GameOutcome
	StartedAtTick   uint64
	EndedAtTick     uint64
	FinalStageIndex uint32
	ClearedStages   []StageSummary
	Players         []PlayerResult
}

func (r GameResult) Clone() GameResult {
	r.ClearedStages = slices.Clone(r.ClearedStages)
	r.Players = slices.Clone(r.Players)
	return r
}

func (r GameResult) DurationTicks() uint64 {
	if r.EndedAtTick < r.StartedAtTick {
		return 0
	}
	return r.EndedAtTick - r.StartedAtTick
}

// GameResult validates the caller's trusted terminal reason against World.
// Victory is legal after a clear (including Reward/PreparingNextStage), defeat
// only after the authoritative Failed transition, and abandonment only while
// a run is otherwise still active.
func (w *World) GameResult(outcome GameOutcome) (GameResult, error) {
	if !outcome.Valid() {
		return GameResult{}, ErrInvalidGameOutcome
	}
	if !w.runStarted {
		return GameResult{}, ErrGameResultState
	}
	endTick := w.tick
	switch outcome {
	case GameVictory:
		if !w.performance.ready || len(w.completedStages) == 0 || (w.stage.State != stage.StageClear && w.stage.State != stage.Reward && w.stage.State != stage.PreparingNextStage) {
			return GameResult{}, ErrGameResultState
		}
		endTick = w.completedStages[len(w.completedStages)-1].ClearTick
	case GameDefeat:
		if w.stage.State != stage.Failed || w.runEndedAtTick == 0 {
			return GameResult{}, ErrGameResultState
		}
		endTick = w.runEndedAtTick
	case GameAbandoned:
		if w.stage.State == stage.Waiting || w.stage.State == stage.Closed || w.stage.State == stage.Failed {
			return GameResult{}, ErrGameResultState
		}
	}
	result := GameResult{Outcome: outcome, StartedAtTick: w.runStartedAtTick, EndedAtTick: endTick,
		FinalStageIndex: w.stage.Index, ClearedStages: slices.Clone(w.completedStages)}
	for _, id := range orderedIDs(w.players) {
		player := w.players[id].player
		result.Players = append(result.Players, PlayerResult{PlayerID: player.ID, Alive: player.Alive, Health: player.Health,
			Stats: player.CurrentStats, Equipment: player.Equipment})
	}
	return result, nil
}
