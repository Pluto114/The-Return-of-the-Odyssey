// Package client implements the real TCP lifecycle used by load-test bots.
package client

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"google.golang.org/protobuf/proto"
)

const (
	magic       = 0x4E52
	version     = 1
	headerSize  = 16
	maxBodySize = 64 * 1024
)

var (
	ErrInvalidSnapshot       = errors.New("invalid authoritative snapshot")
	ErrCombatNotAcknowledged = errors.New("bot received no acknowledged combat input")
	ErrStageNotCleared       = errors.New("bot did not observe a cleared stage")
	ErrTeamDefeated          = errors.New("bot team was defeated")
)

type combatProgress struct {
	mu sync.RWMutex

	snapshots           uint64
	lastAcknowledged    uint32
	firstCombatInputSeq uint32
	stageIndex          uint32
	stageCleared        bool
	aimX                float32
	aimY                float32
	shoot               bool

	projectileSpawns   uint64
	projectileDestroys uint64
	damageEvents       uint64
	deathEvents        uint64
	stageStarts        uint64
}

// Run connects one bot, logs in, matches, sends 30Hz movement/combat intent,
// and succeeds only after an acknowledged snapshot and StageCleared event.
func Run(ctx context.Context, address string, clientID int) error {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	var frameSequence uint32
	send := func(messageType pb.MessageType, message proto.Message) error {
		frameSequence++
		return sendMessage(conn, messageType, frameSequence, message)
	}

	if err := send(pb.MessageType_MSG_LOGIN_REQUEST, &pb.LoginRequest{
		ProtocolVersion: 1,
		Token:           "dev",
		DisplayName:     fmt.Sprintf("bot-%d", clientID),
	}); err != nil {
		return err
	}
	var login pb.LoginResponse
	if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_LOGIN_RESPONSE, &login); err != nil {
		return err
	}
	if login.Reason != pb.ReasonCode_REASON_OK {
		return fmt.Errorf("login rejected: %s", login.Reason)
	}
	if err := send(pb.MessageType_MSG_MATCH_REQUEST, &pb.MatchRequest{}); err != nil {
		return err
	}
	var found pb.MatchFound
	if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_MATCH_FOUND, &found); err != nil {
		return err
	}
	if found.RoomId == 0 {
		return errors.New("match returned zero room id")
	}
	_ = conn.SetReadDeadline(time.Time{})

	inputTicker := time.NewTicker(time.Second / 30)
	defer inputTicker.Stop()
	readErrors := make(chan error, 1)
	stateChanges := make(chan struct{}, 1)
	progress := &combatProgress{aimX: 1}
	go func() {
		for {
			messageType, body, err := readFrame(reader)
			if err != nil {
				readErrors <- err
				return
			}
			if err := progress.observe(messageType, body); err != nil {
				readErrors <- err
				return
			}
			select {
			case stateChanges <- struct{}{}:
			default:
			}
		}
	}()
	var inputSequence uint32
	for {
		select {
		case <-ctx.Done():
			if progress.complete() {
				return nil
			}
			if !progress.hasAcknowledgedCombat() {
				return fmt.Errorf("%w before shutdown: %v", ErrCombatNotAcknowledged, ctx.Err())
			}
			return fmt.Errorf("%w before shutdown: %v", ErrStageNotCleared, ctx.Err())
		case err := <-readErrors:
			return err
		case <-stateChanges:
			if progress.complete() {
				return nil
			}
		case now := <-inputTicker.C:
			inputSequence++
			direction := float32(1)
			if clientID%2 != 0 {
				direction = -1
			}
			aim, shoot := progress.combatIntent()
			if err := send(pb.MessageType_MSG_PLAYER_INPUT, &pb.PlayerInput{
				InputSeq:     inputSequence,
				ClientTickMs: uint64(now.UnixMilli()),
				Move:         &pb.Vec2{X: direction},
				Aim:          aim,
				Shoot:        shoot,
			}); err != nil {
				return err
			}
			progress.recordInput(inputSequence, shoot)
			if progress.complete() {
				return nil
			}
		}
	}
}

func (p *combatProgress) observe(messageType pb.MessageType, body []byte) error {
	switch messageType {
	case pb.MessageType_MSG_DISCONNECT:
		var disconnect pb.Disconnect
		if err := proto.Unmarshal(body, &disconnect); err != nil {
			return err
		}
		return fmt.Errorf("server disconnected: %s (%s)", disconnect.Reason, disconnect.Message)
	case pb.MessageType_MSG_WORLD_SNAPSHOT:
		var snapshot pb.WorldSnapshot
		if err := proto.Unmarshal(body, &snapshot); err != nil {
			return err
		}
		return p.observeSnapshot(&snapshot)
	case pb.MessageType_MSG_PROJECTILE_SPAWN:
		var event pb.ProjectileSpawnEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.ProjectileId == 0 {
			return errors.New("invalid projectile spawn event")
		}
		p.mu.Lock()
		p.projectileSpawns++
		p.mu.Unlock()
	case pb.MessageType_MSG_PROJECTILE_DESTROY:
		var event pb.ProjectileDestroyEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.ProjectileId == 0 {
			return errors.New("invalid projectile destroy event")
		}
		p.mu.Lock()
		p.projectileDestroys++
		p.mu.Unlock()
	case pb.MessageType_MSG_DAMAGE_EVENT:
		var event pb.DamageEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.SourceId == 0 || event.TargetId == 0 ||
			event.Amount <= 0 || !finite(event.Amount) || !finite(event.RemainingHealth) {
			return errors.New("invalid damage event")
		}
		p.mu.Lock()
		p.damageEvents++
		p.mu.Unlock()
	case pb.MessageType_MSG_DEATH_EVENT:
		var event pb.DeathEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.EntityId == 0 {
			return errors.New("invalid death event")
		}
		p.mu.Lock()
		p.deathEvents++
		p.mu.Unlock()
	case pb.MessageType_MSG_STAGE_STARTED_EVENT:
		var event pb.StageStartedEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.StageIndex == 0 {
			return errors.New("invalid stage started event")
		}
		p.mu.Lock()
		p.stageIndex = event.StageIndex
		p.stageStarts++
		p.mu.Unlock()
	case pb.MessageType_MSG_STAGE_CLEARED_EVENT:
		var event pb.StageClearedEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.StageIndex == 0 {
			return errors.New("invalid stage cleared event")
		}
		p.mu.Lock()
		if p.stageStarts == 0 {
			p.mu.Unlock()
			return errors.New("stage cleared before a stage started event")
		}
		if p.stageIndex != 0 && p.stageIndex != event.StageIndex {
			p.mu.Unlock()
			return fmt.Errorf("stage cleared index %d does not match observed stage %d", event.StageIndex, p.stageIndex)
		}
		p.stageIndex = event.StageIndex
		p.stageCleared = true
		p.shoot = false
		p.mu.Unlock()
	case pb.MessageType_MSG_TEAM_DEFEATED_EVENT:
		var event pb.TeamDefeatedEvent
		if err := proto.Unmarshal(body, &event); err != nil || event.StageIndex == 0 {
			return errors.New("invalid team defeated event")
		}
		return fmt.Errorf("%w at stage %d tick %d", ErrTeamDefeated, event.StageIndex, event.ServerTick)
	}
	return nil
}

func (p *combatProgress) observeSnapshot(snapshot *pb.WorldSnapshot) error {
	if snapshot.Self == nil || snapshot.Self.Position == nil || !finiteVec(snapshot.Self.Position) {
		return ErrInvalidSnapshot
	}

	aimX := float32(1)
	aimY := float32(0)
	shoot := false
	if snapshot.Self.Alive {
		var bestDistance float64
		var bestID uint64
		for _, monster := range snapshot.Monsters {
			if monster == nil || monster.MonsterId == 0 || monster.Position == nil || !finiteVec(monster.Position) || !finite(monster.Hp) {
				return ErrInvalidSnapshot
			}
			if monster.Hp <= 0 || monster.State == 3 {
				continue
			}
			dx := monster.Position.X - snapshot.Self.Position.X
			dy := monster.Position.Y - snapshot.Self.Position.Y
			distance := float64(dx)*float64(dx) + float64(dy)*float64(dy)
			if !shoot || distance < bestDistance || (distance == bestDistance && monster.MonsterId < bestID) {
				aimX = dx
				aimY = dy
				bestDistance = distance
				bestID = monster.MonsterId
				shoot = true
			}
		}
	}
	if shoot && aimX == 0 && aimY == 0 {
		if snapshot.Self.Aim != nil && finiteVec(snapshot.Self.Aim) && (snapshot.Self.Aim.X != 0 || snapshot.Self.Aim.Y != 0) {
			aimX = snapshot.Self.Aim.X
			aimY = snapshot.Self.Aim.Y
		} else {
			aimX = 1
			aimY = 0
		}
	}

	p.mu.Lock()
	p.snapshots++
	if snapshot.LastProcessedInput > p.lastAcknowledged {
		p.lastAcknowledged = snapshot.LastProcessedInput
	}
	if snapshot.Stage != nil && snapshot.Stage.Index != 0 {
		p.stageIndex = snapshot.Stage.Index
	}
	p.aimX = aimX
	p.aimY = aimY
	p.shoot = shoot
	p.mu.Unlock()
	return nil
}

func (p *combatProgress) combatIntent() (*pb.Vec2, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return &pb.Vec2{X: p.aimX, Y: p.aimY}, p.shoot
}

func (p *combatProgress) recordInput(sequence uint32, combat bool) {
	if !combat {
		return
	}
	p.mu.Lock()
	if p.firstCombatInputSeq == 0 {
		p.firstCombatInputSeq = sequence
	}
	p.mu.Unlock()
}

func (p *combatProgress) hasAcknowledgedCombat() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshots > 0 && p.firstCombatInputSeq > 0 && p.lastAcknowledged >= p.firstCombatInputSeq
}

func (p *combatProgress) complete() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshots > 0 && p.firstCombatInputSeq > 0 && p.lastAcknowledged >= p.firstCombatInputSeq && p.stageCleared
}

func finiteVec(vector *pb.Vec2) bool {
	return vector != nil && finite(vector.X) && finite(vector.Y)
}

func finite(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func sendMessage(conn net.Conn, messageType pb.MessageType, sequence uint32, message proto.Message) error {
	body, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	if len(body) > maxBodySize {
		return errors.New("message exceeds body limit")
	}
	frame := make([]byte, headerSize+len(body))
	binary.BigEndian.PutUint16(frame[0:2], magic)
	frame[2] = version
	binary.BigEndian.PutUint16(frame[4:6], uint16(messageType))
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(body)))
	binary.BigEndian.PutUint32(frame[12:16], sequence)
	copy(frame[headerSize:], body)
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(frame)
	return err
}

func readUntil(ctx context.Context, conn net.Conn, reader *bufio.Reader, want pb.MessageType, out proto.Message) error {
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetReadDeadline(deadline)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		messageType, body, err := readFrame(reader)
		if err != nil {
			return err
		}
		if messageType == pb.MessageType_MSG_DISCONNECT {
			var disconnect pb.Disconnect
			_ = proto.Unmarshal(body, &disconnect)
			return fmt.Errorf("server disconnected: %s (%s)", disconnect.Reason, disconnect.Message)
		}
		if messageType == want {
			return proto.Unmarshal(body, out)
		}
	}
}

func readFrame(reader *bufio.Reader) (pb.MessageType, []byte, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	if binary.BigEndian.Uint16(header[0:2]) != magic || header[2] != version || header[3] != 0 ||
		binary.BigEndian.Uint16(header[6:8]) != 0 {
		return 0, nil, errors.New("invalid frame header")
	}
	length := binary.BigEndian.Uint32(header[8:12])
	if length > maxBodySize {
		return 0, nil, errors.New("frame body exceeds limit")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return pb.MessageType(binary.BigEndian.Uint16(header[4:6])), body, nil
}
