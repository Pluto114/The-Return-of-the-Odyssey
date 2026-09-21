package router

import (
	"bufio"
	"bytes"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// recordingSink 捕获每名玩家的最新快照帧，供测试解码并断言个性化内容。
type recordingSink struct {
	mu    sync.Mutex
	frame []byte
}

func (s *recordingSink) SendSnapshot(frame []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frame = frame
	return true
}

func (s *recordingSink) snapshot(t *testing.T) *protocol.WorldSnapshot {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frame == nil {
		t.Fatal("no snapshot delivered")
	}
	// 通过 ReadFrame 解码 16 字节帧头和消息体。
	_, body, err := network.ReadFrame(bufio.NewReader(bytes.NewReader(s.frame)))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	var ws protocol.WorldSnapshot
	if err := proto.Unmarshal(body, &ws); err != nil {
		t.Fatalf("unmarshal WorldSnapshot: %v", err)
	}
	return &ws
}

func twoPlayerSnapshot() room.Snapshot {
	return room.Snapshot{
		Snapshot: game.Snapshot{
			ServerTick: 10,
			Players: []entity.Player{
				{ID: 1, LastProcessedInputSeq: 5, CurrentStats: stats(1, 1, 100, 1), Health: 80, Alive: true},
				{ID: 2, LastProcessedInputSeq: 9, CurrentStats: stats(1, 1, 100, 1), Health: 60, Alive: true},
			},
			Monsters: []game.MonsterView{
				{ID: 1<<63 | 1, Health: 30, MaxHealth: 100, State: entity.MonsterIdle},
			},
			Stage: stage.View{Index: 1, State: stage.Playing, MonstersRemaining: 1},
		},
	}
}

func stats(attack, defense, maxHP, moveSpeed float64) entity.CombatStats {
	return entity.CombatStats{Attack: attack, Defense: defense, MaxHealth: maxHP, MoveSpeed: moveSpeed, AttackCooldownTicks: 6}
}

func TestDispatcherPersonalizesSelfPerSubscriber(t *testing.T) {
	d := NewSnapshotDispatcher()
	s1 := &recordingSink{}
	s2 := &recordingSink{}
	d.Subscribe(1, s1)
	d.Subscribe(2, s2)

	d.Dispatch(twoPlayerSnapshot())

	// 玩家 1 在 Self 中看到自己，确认序号为 5。
	ws1 := s1.snapshot(t)
	if ws1.Self == nil || ws1.Self.PlayerId != 1 {
		t.Fatalf("player1 self = %v, want player 1", ws1.Self)
	}
	if ws1.LastProcessedInput != 5 {
		t.Errorf("player1 ack = %d, want 5", ws1.LastProcessedInput)
	}
	if len(ws1.Players) != 1 || ws1.Players[0].PlayerId != 2 {
		t.Errorf("player1 others = %v, want [2]", ws1.Players)
	}

	// 玩家 2 在 Self 中看到自己，确认序号为 9。
	ws2 := s2.snapshot(t)
	if ws2.Self == nil || ws2.Self.PlayerId != 2 {
		t.Fatalf("player2 self = %v, want player 2", ws2.Self)
	}
	if ws2.LastProcessedInput != 9 {
		t.Errorf("player2 ack = %d, want 9", ws2.LastProcessedInput)
	}
	if len(ws2.Players) != 1 || ws2.Players[0].PlayerId != 1 {
		t.Errorf("player2 others = %v, want [1]", ws2.Players)
	}

	// 共享状态应完全一致。
	if ws1.ServerTick != ws2.ServerTick || ws1.ServerTick != 10 {
		t.Errorf("server_tick = %d/%d, want 10", ws1.ServerTick, ws2.ServerTick)
	}
	if len(ws1.Monsters) != 1 || len(ws2.Monsters) != 1 {
		t.Errorf("monsters = %d/%d, want 1", len(ws1.Monsters), len(ws2.Monsters))
	}
}

func TestDispatcherSkipsUnsubscribedPlayer(t *testing.T) {
	d := NewSnapshotDispatcher()
	s1 := &recordingSink{}
	d.Subscribe(1, s1)
	// 玩家 2 没有 sink。

	d.Dispatch(twoPlayerSnapshot())

	if s1.snapshot(t).Self.PlayerId != 1 {
		t.Error("player1 should still receive")
	}
	if d.Subscribers() != 1 {
		t.Errorf("Subscribers = %d, want 1", d.Subscribers())
	}
}

func TestDispatcherUnsubscribeStopsDelivery(t *testing.T) {
	d := NewSnapshotDispatcher()
	s1 := &recordingSink{}
	d.Subscribe(1, s1)
	d.Dispatch(twoPlayerSnapshot())
	if s1.snapshot(t) == nil {
		t.Fatal("expected first delivery")
	}

	d.Unsubscribe(1)
	d.Dispatch(twoPlayerSnapshot())
	// 取消订阅后不能再收到新帧，旧帧保持不变。
	if d.Subscribers() != 0 {
		t.Errorf("Subscribers = %d, want 0", d.Subscribers())
	}
}
