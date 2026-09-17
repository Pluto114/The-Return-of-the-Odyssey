package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/persistence"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// A 128-bit random match ID is allocated with the room and reused across
// delivery retries. Unlike process-local room IDs, it remains collision-
// resistant across server restarts.
func newMatchID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate match id: %w", err)
	}
	return "match-" + hex.EncodeToString(value[:]), nil
}

// submitGameResult runs off the room Tick and the socket reader. The World
// provides a detached authoritative result; the writer admits it without
// waiting for MySQL. Duplicate terminal events do not enqueue a second result.
func (a *gameApplication) submitGameResult(roomID room.ID, outcome game.GameOutcome) {
	a.submitGameResultAfterCapture(roomID, outcome, nil)
}

// afterCapture releases the last disconnect's Room membership once the result
// no longer depends on live World state. It also runs when capture is refused.
func (a *gameApplication) submitGameResultAfterCapture(roomID room.ID, outcome game.GameOutcome, afterCapture func()) {
	defer func() {
		if afterCapture != nil {
			afterCapture()
		}
	}()
	a.mu.Lock()
	active := a.rooms[roomID]
	writer := a.resultWriter
	if active == nil || writer == nil || active.resultSubmitting || active.resultQueued {
		a.mu.Unlock()
		return
	}
	active.resultSubmitting = true
	a.mu.Unlock()
	queued := false
	defer func() {
		a.mu.Lock()
		active.resultSubmitting = false
		if queued {
			active.resultQueued = true
		}
		a.mu.Unlock()
	}()

	var result game.GameResult
	for {
		receipt, err := active.room.GameResult(outcome)
		if errors.Is(err, room.ErrQueueFull) {
			if !a.waitResultRetry(active.room.Done()) {
				a.logger.Error("result room admission stopped", "room_id", roomID, "match_id", active.matchID)
				return
			}
			continue
		}
		if err != nil {
			a.logger.Error("result capture rejected", "room_id", roomID, "match_id", active.matchID, "err", err)
			return
		}
		select {
		case delivered := <-receipt:
			if delivered.Err != nil {
				a.logger.Error("result capture failed", "room_id", roomID, "match_id", active.matchID, "err", delivered.Err)
				return
			}
			result = delivered.Result
		case <-active.room.Done():
			a.logger.Error("room closed before result capture", "room_id", roomID, "match_id", active.matchID)
			return
		case <-a.ctx.Done():
			return
		}
		break
	}
	if afterCapture != nil {
		afterCapture()
		afterCapture = nil
	}
	// A final disconnect can overtake delivery of the last StageCleared event.
	// The copied World history is authoritative: a cleared final stage is a
	// victory even when its event observer has not yet updated application state.
	result = terminalResultOnDeparture(result, a.gameplay.StageLimit())
	envelope := persistence.ResultEnvelope{MatchID: active.matchID, RoomID: uint64(roomID),
		CreatedAt: active.createdAt, Result: result}
	for {
		err := writer.Submit(envelope)
		if err == nil {
			queued = true
			a.logger.Info("terminal result queued", "room_id", roomID, "match_id", active.matchID, "outcome", result.Outcome.String())
			return
		}
		if !errors.Is(err, persistence.ErrResultQueueFull) {
			a.logger.Error("terminal result rejected", "room_id", roomID, "match_id", active.matchID, "err", err)
			return
		}
		// A copied result no longer depends on the Room staying alive.
		if !a.waitResultRetry(nil) {
			a.logger.Error("result queue never accepted terminal result", "room_id", roomID, "match_id", active.matchID)
			return
		}
	}
}

func terminalResultOnDeparture(result game.GameResult, stageLimit uint32) game.GameResult {
	if result.Outcome == game.GameAbandoned && stageLimit > 0 && result.FinalStageIndex == stageLimit &&
		len(result.ClearedStages) > 0 && result.ClearedStages[len(result.ClearedStages)-1].Index == stageLimit {
		result.Outcome = game.GameVictory
		result.EndedAtTick = result.ClearedStages[len(result.ClearedStages)-1].ClearTick
	}
	return result
}

func (a *gameApplication) waitResultRetry(roomDone <-chan struct{}) bool {
	select {
	case <-a.ctx.Done():
		return false
	case <-roomDone:
		return false
	case <-time.After(20 * time.Millisecond):
		return true
	}
}
