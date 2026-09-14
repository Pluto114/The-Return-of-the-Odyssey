package room_test

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func TestRoomResumeStateAndGameResultUseOwnerQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 77, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		receipt, err := r.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if err := r.Input(11, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(1)
		if r.LatestSnapshot().Stage.State != stage.StageClear {
			t.Fatal("test stage did not clear")
		}
		receipt, err = r.StartReward(roomRewardCatalog(t), 44, 30)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)

		resumeReceipt, err := r.ResumeState(11)
		if err != nil {
			t.Fatal(err)
		}
		resumed := <-resumeReceipt
		if resumed.Err != nil || resumed.State.Snapshot.RoomID != 77 || resumed.State.Snapshot.Stage.State != stage.Reward ||
			len(resumed.State.Snapshot.Players) != 1 || resumed.State.Snapshot.Players[0].ID != 101 || resumed.State.Reward == nil ||
			resumed.State.Reward.Kind != game.RewardOptionsAvailable || resumed.State.Reward.PlayerID != 101 {
			t.Fatalf("resume receipt = %+v", resumed)
		}
		if _, open := <-resumeReceipt; open {
			t.Fatal("resume receipt did not close")
		}
		resumed.State.Snapshot.Players[0].Health = -1
		resumed.State.Reward.EquipmentIDs[0] = 999
		againReceipt, err := r.ResumeState(11)
		if err != nil {
			t.Fatal(err)
		}
		again := <-againReceipt
		if again.Err != nil || again.State.Snapshot.Players[0].Health < 0 || again.State.Reward.EquipmentIDs[0] == 999 {
			t.Fatalf("resume receipt aliased room: %+v", again)
		}

		missingReceipt, err := r.ResumeState(99)
		if err != nil {
			t.Fatal(err)
		}
		if missing := <-missingReceipt; !errors.Is(missing.Err, room.ErrNotJoined) {
			t.Fatalf("unknown resume session returned %+v", missing)
		}
		if _, err := r.ResumeState(0); !errors.Is(err, room.ErrInvalidSession) {
			t.Fatalf("zero resume session returned %v", err)
		}

		gameReceipt, err := r.GameResult(game.GameVictory)
		if err != nil {
			t.Fatal(err)
		}
		gameResult := <-gameReceipt
		if gameResult.Err != nil || gameResult.Result.Outcome != game.GameVictory || gameResult.Result.FinalStageIndex != 1 || len(gameResult.Result.Players) != 1 {
			t.Fatalf("game result receipt = %+v", gameResult)
		}
		if _, open := <-gameReceipt; open {
			t.Fatal("game result receipt did not close")
		}
		if _, err := r.GameResult(game.GameOutcome(99)); !errors.Is(err, game.ErrInvalidGameOutcome) {
			t.Fatalf("invalid game outcome returned %v", err)
		}
	})
}

func TestResumeDoesNotReplayOrRollbackWorld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		if err := r.Input(11, game.Input{Seq: 10, Direction: entity.Vec2{X: 1}}); err != nil {
			t.Fatal(err)
		}
		advance(1)
		beforeReceipt, err := r.ResumeState(11)
		if err != nil {
			t.Fatal(err)
		}
		before := (<-beforeReceipt).State.Snapshot.Players[0]
		if err := r.Input(11, game.Input{Seq: 10, Direction: entity.Vec2{X: -1}}); err != nil {
			t.Fatal(err)
		}
		advance(1)
		afterReceipt, err := r.ResumeState(11)
		if err != nil {
			t.Fatal(err)
		}
		after := (<-afterReceipt).State.Snapshot.Players[0]
		if after.LastProcessedInputSeq != before.LastProcessedInputSeq || after.Position.X < before.Position.X {
			t.Fatalf("stale resume input rolled back world: before=%+v after=%+v", before, after)
		}
	})
}

func TestCloseCompletesPendingStateQueries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		resumeReceipt, err := r.ResumeState(11)
		if err != nil {
			t.Fatal(err)
		}
		gameReceipt, err := r.GameResult(game.GameVictory)
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		if result := <-resumeReceipt; !errors.Is(result.Err, room.ErrClosed) {
			t.Fatalf("pending resume receipt = %+v", result)
		}
		if result := <-gameReceipt; !errors.Is(result.Err, room.ErrClosed) {
			t.Fatalf("pending game result receipt = %+v", result)
		}
	})
}
