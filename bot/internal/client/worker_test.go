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

func TestBotReloadIntentFollowsAuthoritativeMagazine(t *testing.T) {
	p := &combatProgress{}
	snapshot := combatSnapshot(1)
	snapshot.Self.MagazineCapacity = 12
	snapshot.Self.Ammo = 0
	snapshot.Stage = &pb.StageState{Index: 4, DifficultyScore: 1.7, DifficultyAdjustment: 0.13}
	if err := p.observeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if !p.reloadIntent() {
		t.Fatal("empty magazine must request reload")
	}
	snapshot.Self.ReloadTicksRemaining = 44
	if err := p.observeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if p.reloadIntent() {
		t.Fatal("must not restart active reload")
	}
	snapshot.Self.ReloadTicksRemaining = 0
	snapshot.Self.Ammo = 12
	if err := p.observeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if p.reloadIntent() {
		t.Fatal("full magazine requested reload")
	}
	reloads, samples := p.ammoDiagnostics()
	if reloads != 1 || len(samples) != 1 || samples[0].Stage != 4 {
		t.Fatalf("wrong diagnostics: %d/%v", reloads, samples)
	}
	samples[0].Stage = 99
	_, again := p.ammoDiagnostics()
	if again[0].Stage != 4 {
		t.Fatal("diagnostics leaked mutable state")
	}
}

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
	result, err := RunMatch(ctx, address, 0, MatchConfig{TargetStages: 1, RewardScenario: RewardNormal})
	if err != nil {
		t.Fatalf("RunMatch() error = %v", err)
	}
	if result.Phase != PhaseComplete || result.StagesCleared != 1 || result.StageIndex != 1 {
		t.Fatalf("RunMatch() result = %+v", result)
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
		if err := sendMessage(conn, pb.MessageType_MSG_STAGE_STARTED_EVENT, 3, &pb.StageStartedEvent{StageIndex: 1, ServerTick: 1}); err != nil {
			return err
		}
		if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, 4, combatSnapshot(0)); err != nil {
			return err
		}
		var firstInput pb.PlayerInput
		if err := readMessage(reader, pb.MessageType_MSG_PLAYER_INPUT, &firstInput); err != nil {
			return err
		}
		if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, 5, combatSnapshot(firstInput.InputSeq)); err != nil {
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

func TestRunMatchCompletesThreeStagesWithRewardsReadyAndPotion(t *testing.T) {
	config := DefaultMatchConfig()
	config.Seed = 77
	config.EnableResume = false
	config.MaxResumeAttempts = 0

	address, serverErrors, stop := startFakeServer(t, func(conn net.Conn, reader *bufio.Reader) error {
		if err := handshakeBot(conn, reader); err != nil {
			return err
		}
		var frameSequence uint32 = 2
		for stageIndex := uint32(1); stageIndex <= config.TargetStages; stageIndex++ {
			frameSequence++
			if err := sendMessage(conn, pb.MessageType_MSG_STAGE_STARTED_EVENT, frameSequence, &pb.StageStartedEvent{
				StageIndex: stageIndex, ServerTick: uint64(stageIndex*100 + 1),
			}); err != nil {
				return err
			}
			snapshot := combatSnapshot(0)
			snapshot.ServerTick = uint64(stageIndex*100 + 2)
			snapshot.Stage.Index = stageIndex
			frameSequence++
			if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, frameSequence, snapshot); err != nil {
				return err
			}

			var input pb.PlayerInput
			if err := readMessage(reader, pb.MessageType_MSG_PLAYER_INPUT, &input); err != nil {
				return err
			}
			if !input.Shoot || input.UsePotion != (stageIndex > 1) {
				return fmt.Errorf("stage %d input shoot/potion = %v/%v", stageIndex, input.Shoot, input.UsePotion)
			}
			snapshot.LastProcessedInput = input.InputSeq
			snapshot.ServerTick++
			frameSequence++
			if err := sendMessage(conn, pb.MessageType_MSG_WORLD_SNAPSHOT, frameSequence, snapshot); err != nil {
				return err
			}
			frameSequence++
			if err := sendMessage(conn, pb.MessageType_MSG_STAGE_CLEARED_EVENT, frameSequence, &pb.StageClearedEvent{
				StageIndex: stageIndex, ServerTick: snapshot.ServerTick + 1,
			}); err != nil {
				return err
			}
			if stageIndex == config.TargetStages {
				continue
			}

			ids := []uint32{1001, 2001, 3001}
			frameSequence++
			if err := sendMessage(conn, pb.MessageType_MSG_REWARD_OPTIONS, frameSequence, &pb.RewardOptions{
				StageIndex: stageIndex, EquipmentIds: ids, DeadlineServerTick: snapshot.ServerTick + 100,
			}); err != nil {
				return err
			}
			var choice pb.RewardChoice
			if err := readMessage(reader, pb.MessageType_MSG_REWARD_CHOICE, &choice); err != nil {
				return err
			}
			want := deterministicRewardChoice(config.Seed, 0, stageIndex, ids)
			if choice.EquipmentId != want {
				return fmt.Errorf("stage %d choice = %d, want %d", stageIndex, choice.EquipmentId, want)
			}
			frameSequence++
			if err := sendMessage(conn, pb.MessageType_MSG_REWARD_APPLIED, frameSequence, &pb.RewardApplied{
				Reason: pb.ReasonCode_REASON_OK, EquipmentId: choice.EquipmentId,
			}); err != nil {
				return err
			}
			var ready pb.NextStageRequest
			if err := readMessage(reader, pb.MessageType_MSG_NEXT_STAGE_REQUEST, &ready); err != nil {
				return err
			}
		}
		return nil
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := RunMatch(ctx, address, 0, config)
	if err != nil {
		t.Fatalf("RunMatch() error = %v; result = %+v", err, result)
	}
	if result.Phase != PhaseComplete || result.StagesCleared != 3 || result.RewardsApplied != 2 || result.ReadySent != 2 || result.PotionInputs != 2 {
		t.Fatalf("RunMatch() result = %+v", result)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("fake server error = %v", err)
	}
}

func TestLifecycleRewardScenarios(t *testing.T) {
	for _, scenario := range []RewardScenario{RewardNormal, RewardInvalid, RewardDuplicate, RewardTimeout} {
		t.Run(string(scenario), func(t *testing.T) {
			config := DefaultMatchConfig()
			config.RewardScenario = scenario
			progress := newLifecycleProgress(config, 3, 9)
			progress.phase = PhaseReward
			progress.stagesCleared = 1
			progress.combat.stageIndex = 1
			progress.combat.lastServerTick = 10

			// The deadline tick itself remains a valid choice boundary.
			options := &pb.RewardOptions{StageIndex: 1, EquipmentIds: []uint32{1001, 2001, 3001}, DeadlineServerTick: 10}
			body, err := proto.Marshal(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := progress.observe(pb.MessageType_MSG_REWARD_OPTIONS, body); err != nil {
				t.Fatal(err)
			}

			sendApplied := func(reason pb.ReasonCode, id uint32) {
				t.Helper()
				body, err := proto.Marshal(&pb.RewardApplied{Reason: reason, EquipmentId: id})
				if err != nil {
					t.Fatal(err)
				}
				if err := progress.observe(pb.MessageType_MSG_REWARD_APPLIED, body); err != nil {
					t.Fatal(err)
				}
			}

			switch scenario {
			case RewardNormal:
				action, ok := progress.nextAction()
				if !ok || action.message.(*pb.RewardChoice).EquipmentId != progress.validRewardChoice {
					t.Fatalf("normal action = %+v/%v", action, ok)
				}
				sendApplied(pb.ReasonCode_REASON_OK, progress.validRewardChoice)
			case RewardInvalid:
				action, ok := progress.nextAction()
				if !ok || action.message.(*pb.RewardChoice).EquipmentId == progress.validRewardChoice {
					t.Fatalf("invalid action = %+v/%v", action, ok)
				}
				sendApplied(pb.ReasonCode_REASON_INVALID_STATE, action.message.(*pb.RewardChoice).EquipmentId)
				action, ok = progress.nextAction()
				if !ok || action.message.(*pb.RewardChoice).EquipmentId != progress.validRewardChoice {
					t.Fatalf("fallback action = %+v/%v", action, ok)
				}
				sendApplied(pb.ReasonCode_REASON_OK, progress.validRewardChoice)
			case RewardDuplicate:
				first, firstOK := progress.nextAction()
				second, secondOK := progress.nextAction()
				if !firstOK || !secondOK || first.message.(*pb.RewardChoice).EquipmentId != second.message.(*pb.RewardChoice).EquipmentId {
					t.Fatalf("duplicate actions = %+v/%v %+v/%v", first, firstOK, second, secondOK)
				}
				sendApplied(pb.ReasonCode_REASON_OK, progress.validRewardChoice)
				sendApplied(pb.ReasonCode_REASON_INVALID_STATE, progress.validRewardChoice)
			case RewardTimeout:
				if action, ok := progress.nextAction(); ok {
					t.Fatalf("timeout unexpectedly queued %+v", action)
				}
				sendApplied(pb.ReasonCode_REASON_OK, options.EquipmentIds[0])
			}

			ready, ok := progress.nextAction()
			if !ok || ready.kind != actionReady || progress.rewardsApplied != 1 || progress.phase != PhaseReady {
				t.Fatalf("ready/result = %+v/%v rewards=%d phase=%s", ready, ok, progress.rewardsApplied, progress.phase)
			}
		})
	}
}

func TestMatchConfigRejectsInvalidLifecycleSettings(t *testing.T) {
	for _, config := range []MatchConfig{
		{},
		{TargetStages: 3, RewardScenario: "unknown"},
		{TargetStages: 3, RewardScenario: RewardNormal, EnableResume: true},
		{TargetStages: 1001, RewardScenario: RewardNormal},
	} {
		if err := config.Validate(); !errors.Is(err, ErrInvalidMatchConfig) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalidMatchConfig", config, err)
		}
	}
}

func TestRunMatchResumesAfterTransientDisconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErrors := make(chan error, 1)
	go func() {
		first, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		reader := bufio.NewReader(first)
		var login pb.LoginRequest
		if err := readMessage(reader, pb.MessageType_MSG_LOGIN_REQUEST, &login); err != nil {
			serverErrors <- err
			return
		}
		if err := sendMessage(first, pb.MessageType_MSG_LOGIN_RESPONSE, 1, &pb.LoginResponse{
			Reason: pb.ReasonCode_REASON_OK, SessionId: 7, PlayerId: 8, ResumeToken: []byte("resume-once"),
		}); err != nil {
			serverErrors <- err
			return
		}
		var match pb.MatchRequest
		if err := readMessage(reader, pb.MessageType_MSG_MATCH_REQUEST, &match); err != nil {
			serverErrors <- err
			return
		}
		if err := sendMessage(first, pb.MessageType_MSG_MATCH_FOUND, 2, &pb.MatchFound{RoomId: 9}); err != nil {
			serverErrors <- err
			return
		}
		if err := sendMessage(first, pb.MessageType_MSG_STAGE_STARTED_EVENT, 3, &pb.StageStartedEvent{StageIndex: 1, ServerTick: 1}); err != nil {
			serverErrors <- err
			return
		}
		if err := sendMessage(first, pb.MessageType_MSG_WORLD_SNAPSHOT, 4, combatSnapshot(0)); err != nil {
			serverErrors <- err
			return
		}
		var input pb.PlayerInput
		if err := readMessage(reader, pb.MessageType_MSG_PLAYER_INPUT, &input); err != nil {
			serverErrors <- err
			return
		}
		_ = first.Close()

		second, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer second.Close()
		reader = bufio.NewReader(second)
		var resume pb.ResumeRequest
		if err := readMessage(reader, pb.MessageType_MSG_RESUME_REQUEST, &resume); err != nil {
			serverErrors <- err
			return
		}
		if string(resume.ResumeToken) != "resume-once" || resume.ProtocolVersion != 2 {
			serverErrors <- fmt.Errorf("resume request = %+v", &resume)
			return
		}
		if err := sendMessage(second, pb.MessageType_MSG_RESUME_RESPONSE, 1, &pb.ResumeResponse{
			Reason: pb.ReasonCode_REASON_OK, SessionId: 7, PlayerId: 8,
		}); err != nil {
			serverErrors <- err
			return
		}
		snapshot := combatSnapshot(input.InputSeq)
		snapshot.ServerTick = 5
		if err := sendMessage(second, pb.MessageType_MSG_WORLD_SNAPSHOT, 2, snapshot); err != nil {
			serverErrors <- err
			return
		}
		if err := sendMessage(second, pb.MessageType_MSG_STAGE_CLEARED_EVENT, 3, &pb.StageClearedEvent{StageIndex: 1, ServerTick: 6}); err != nil {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	config := MatchConfig{TargetStages: 1, RewardScenario: RewardNormal, EnableResume: true, MaxResumeAttempts: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := RunMatch(ctx, listener.Addr().String(), 0, config)
	if err != nil {
		t.Fatalf("RunMatch() error = %v; result = %+v", err, result)
	}
	if result.Phase != PhaseComplete || result.RoomID != 9 || result.RecoveryAttempts != 1 || result.RecoverySucceeded != 1 || result.LastServerTick != 6 {
		t.Fatalf("RunMatch() result = %+v", result)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
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
