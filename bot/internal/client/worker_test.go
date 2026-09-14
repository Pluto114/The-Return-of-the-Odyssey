package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"google.golang.org/protobuf/proto"
)

func TestRunAimsShootsAndRequiresStageClear(t *testing.T) {
	address, serverErrors, stop := startFakeServer(t, func(conn net.Conn, reader *bufio.Reader) error {
		if err := handshakeBot(conn, reader); err != nil {
			return err
		}
		if err := sendMessage(conn, pb.MessageType_MSG_STAGE_STARTED_EVENT, 3, &pb.StageStartedEvent{StageIndex: 1, ServerTick: 1}); err != nil {
			return err
		}
		if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, 4, combatSnapshot(0)); err != nil {
			return err
		}

		var input pb.PlayerInput
		if err := readMessage(reader, pb.MessageType_MSG_PLAYER_INPUT, &input); err != nil {
			return err
		}
		if !input.Shoot || input.Aim == nil || input.Aim.X != 3 || input.Aim.Y != 4 {
			return fmt.Errorf("combat input = %+v, want Shoot with Aim(3,4)", &input)
		}
		if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, 5, combatSnapshot(input.InputSeq)); err != nil {
			return err
		}

		events := []struct {
			typeID  pb.MessageType
			message proto.Message
		}{
			{pb.MessageType_MSG_PROJECTILE_SPAWN, &pb.ProjectileSpawnEvent{ProjectileId: 9, OwnerId: 1}},
			{pb.MessageType_MSG_DAMAGE_EVENT, &pb.DamageEvent{SourceId: 1, TargetId: 7, Amount: 10, RemainingHealth: 0}},
			{pb.MessageType_MSG_DEATH_EVENT, &pb.DeathEvent{EntityId: 7, KillerId: 1}},
			{pb.MessageType_MSG_PROJECTILE_DESTROY, &pb.ProjectileDestroyEvent{ProjectileId: 9, OwnerId: 1}},
			{pb.MessageType_MSG_STAGE_CLEARED_EVENT, &pb.StageClearedEvent{StageIndex: 1, ServerTick: 6}},
		}
		for index, event := range events {
			if err := sendMessage(conn, event.typeID, uint32(index+6), event.message); err != nil {
				return err
			}
		}
		return nil
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Run(ctx, address, 0); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("fake server error = %v", err)
	}
}

func TestRunFailsWhenAcknowledgedCombatNeverClears(t *testing.T) {
	address, serverErrors, stop := startFakeServer(t, func(conn net.Conn, reader *bufio.Reader) error {
		if err := handshakeBot(conn, reader); err != nil {
			return err
		}
		if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, 3, combatSnapshot(0)); err != nil {
			return err
		}
		var firstInput pb.PlayerInput
		if err := readMessage(reader, pb.MessageType_MSG_PLAYER_INPUT, &firstInput); err != nil {
			return err
		}
		if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, 4, combatSnapshot(firstInput.InputSeq)); err != nil {
			return err
		}
		for {
			var input pb.PlayerInput
			if err := readMessage(reader, pb.MessageType_MSG_PLAYER_INPUT, &input); err != nil {
				return nil
			}
		}
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := Run(ctx, address, 1)
	if !errors.Is(err, ErrStageNotCleared) {
		t.Fatalf("Run() error = %v, want ErrStageNotCleared", err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("fake server error = %v", err)
	}
}

func TestCombatProgressRejectsDefeatAndChoosesStableNearestTarget(t *testing.T) {
	progress := &combatProgress{aimX: 1}
	snapshot := combatSnapshot(1)
	snapshot.Monsters = append(snapshot.Monsters,
		&pb.MonsterSnapshot{MonsterId: 8, Position: &pb.Vec2{X: 2, Y: 1}, Hp: 10},
		&pb.MonsterSnapshot{MonsterId: 6, Position: &pb.Vec2{X: 2, Y: 1}, Hp: 10},
	)
	if err := progress.observeSnapshot(snapshot); err != nil {
		t.Fatalf("observeSnapshot() error = %v", err)
	}
	aim, shoot := progress.combatIntent()
	if !shoot || aim.X != 1 || aim.Y != 0 {
		t.Fatalf("combatIntent() = (%+v, %v), want ((1,0), true)", aim, shoot)
	}
	progress.recordInput(4, true)
	if progress.complete() {
		t.Fatal("complete() = true before combat input acknowledgment and stage clear")
	}

	body, err := proto.Marshal(&pb.TeamDefeatedEvent{StageIndex: 1, ServerTick: 12})
	if err != nil {
		t.Fatal(err)
	}
	if err := progress.observe(pb.MessageType_MSG_TEAM_DEFEATED_EVENT, body); !errors.Is(err, ErrTeamDefeated) {
		t.Fatalf("observe() error = %v, want ErrTeamDefeated", err)
	}
}

func TestCombatProgressRejectsInvalidEvents(t *testing.T) {
	tests := []struct {
		name    string
		typeID  pb.MessageType
		message proto.Message
	}{
		{"projectile spawn without id", pb.MessageType_MSG_PROJECTILE_SPAWN, &pb.ProjectileSpawnEvent{}},
		{"projectile destroy without id", pb.MessageType_MSG_PROJECTILE_DESTROY, &pb.ProjectileDestroyEvent{}},
		{"damage without source", pb.MessageType_MSG_DAMAGE_EVENT, &pb.DamageEvent{TargetId: 7, Amount: 1}},
		{"non-finite damage", pb.MessageType_MSG_DAMAGE_EVENT, &pb.DamageEvent{SourceId: 1, TargetId: 7, Amount: float32(math.Inf(1))}},
		{"death without entity", pb.MessageType_MSG_DEATH_EVENT, &pb.DeathEvent{}},
		{"stage start without index", pb.MessageType_MSG_STAGE_STARTED_EVENT, &pb.StageStartedEvent{}},
		{"stage clear without start", pb.MessageType_MSG_STAGE_CLEARED_EVENT, &pb.StageClearedEvent{StageIndex: 1}},
		{"defeat without index", pb.MessageType_MSG_TEAM_DEFEATED_EVENT, &pb.TeamDefeatedEvent{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := proto.Marshal(test.message)
			if err != nil {
				t.Fatal(err)
			}
			if err := (&combatProgress{}).observe(test.typeID, body); err == nil {
				t.Fatal("observe() error = nil, want invalid event error")
			}
		})
	}
}

func TestCombatProgressRejectsMismatchedStageClear(t *testing.T) {
	progress := &combatProgress{stageIndex: 1, stageStarts: 1}
	body, err := proto.Marshal(&pb.StageClearedEvent{StageIndex: 2, ServerTick: 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := progress.observe(pb.MessageType_MSG_STAGE_CLEARED_EVENT, body); err == nil {
		t.Fatal("observe() error = nil, want mismatched stage error")
	}
}

func TestCombatProgressRejectsInvalidSnapshot(t *testing.T) {
	snapshot := combatSnapshot(0)
	snapshot.Monsters[0].Position.X = float32(math.NaN())
	if err := (&combatProgress{}).observeSnapshot(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("observeSnapshot() error = %v, want ErrInvalidSnapshot", err)
	}
}

func combatSnapshot(acknowledged uint32) *pb.WorldSnapshot {
	return &pb.WorldSnapshot{
		ServerTick:         2,
		LastProcessedInput: acknowledged,
		Self: &pb.PlayerSnapshot{
			PlayerId: 1,
			Position: &pb.Vec2{X: 1, Y: 1},
			Aim:      &pb.Vec2{X: 1},
			Hp:       100,
			Alive:    true,
		},
		Monsters: []*pb.MonsterSnapshot{{
			MonsterId: 7,
			Position:  &pb.Vec2{X: 4, Y: 5},
			Hp:        10,
		}},
		Stage: &pb.StageState{Index: 1, State: 1, MonstersRemaining: 1},
	}
}

func handshakeBot(conn net.Conn, reader *bufio.Reader) error {
	var login pb.LoginRequest
	if err := readMessage(reader, pb.MessageType_MSG_LOGIN_REQUEST, &login); err != nil {
		return err
	}
	if err := sendMessage(conn, pb.MessageType_MSG_LOGIN_RESPONSE, 1, &pb.LoginResponse{
		Reason: pb.ReasonCode_REASON_OK, SessionId: 1, PlayerId: 1,
	}); err != nil {
		return err
	}
	var match pb.MatchRequest
	if err := readMessage(reader, pb.MessageType_MSG_MATCH_REQUEST, &match); err != nil {
		return err
	}
	return sendMessage(conn, pb.MessageType_MSG_MATCH_FOUND, 2, &pb.MatchFound{RoomId: 1})
}

func readMessage(reader *bufio.Reader, want pb.MessageType, message proto.Message) error {
	messageType, body, err := readFrame(reader)
	if err != nil {
		return err
	}
	if messageType != want {
		return fmt.Errorf("message type = %s, want %s", messageType, want)
	}
	return proto.Unmarshal(body, message)
}

func startFakeServer(t *testing.T, serve func(net.Conn, *bufio.Reader) error) (string, <-chan error, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverErrors := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()
		serverErrors <- serve(conn, bufio.NewReader(conn))
	}()
	return listener.Addr().String(), serverErrors, func() { _ = listener.Close() }
}
