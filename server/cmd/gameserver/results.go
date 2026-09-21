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

// 房间创建时分配 128 位随机 matchID，发送重试始终复用；与进程内 roomID 不同，
// 服务重启后仍具备抗碰撞能力。
func newMatchID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate match id: %w", err)
	}
	return "match-" + hex.EncodeToString(value[:]), nil
}

// submitGameResult 在 Room Tick 与 socket Reader 之外运行。World 提供独立权威结果，
// writer 无需等待 MySQL 即可接收；重复终局事件不会重复入队。
func (a *gameApplication) submitGameResult(roomID room.ID, outcome game.GameOutcome) {
	a.submitGameResultAfterCapture(roomID, outcome, nil)
}

// 结果不再依赖 live World 后，afterCapture 释放最后断线玩家的 Room 成员关系；
// 即使结果捕获被拒绝也会执行。
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
	// 最终断线可能早于最后一个 StageCleared 事件到达。复制的 World 历史才是权威：
	// 即使事件观察器尚未更新 application 状态，最终关已通关仍判定胜利。
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
		// 结果复制完成后不再依赖 Room 存活。
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
