// Package lobby 管理匹配队列状态与策略。
package lobby

import (
	"container/list"
	"errors"
	"sync"
)

var (
	ErrInvalidPlayersPerMatch = errors.New("players per match must be positive")
	ErrInvalidPlayerID        = errors.New("player ID must not be empty")
	ErrPlayerAlreadyQueued    = errors.New("player is already queued")
)

// PlayerID 是 Session 模块提供的稳定身份；Lobby 把它视为不透明值且不做归一化。
type PlayerID string

// Match 是可交给 Room 接收的完整 FIFO 小队；Enqueue 返回后 Players 由调用方拥有。
type Match struct {
	Players []PlayerID
}

// Found 表示 Enqueue 是否成功组成完整对局。
func (m Match) Found() bool {
	return len(m.Players) != 0
}

// Matchmaker 按 FIFO 把玩家组成固定人数的小队。多个 Connection Reader 可能同时请求
// 匹配或取消，因此 waiting 链表和 queued 索引必须由同一把 mutex 原子地更新。
// queued 同时用于 O(1) 去重/取消，避免遍历队列。本包不依赖协议、Session 或 Room，
// 匹配完成后只把 PlayerID 列表交给上层创建房间。
type Matchmaker struct {
	mu              sync.Mutex
	playersPerMatch int
	waiting         *list.List
	queued          map[PlayerID]*list.Element
}

// NewMatchmaker 创建空匹配器；人数小于 1 无法形成有效小队，会被拒绝。
func NewMatchmaker(playersPerMatch int) (*Matchmaker, error) {
	if playersPerMatch <= 0 {
		return nil, ErrInvalidPlayersPerMatch
	}

	return &Matchmaker{
		playersPerMatch: playersPerMatch,
		waiting:         list.New(),
		queued:          make(map[PlayerID]*list.Element),
	}, nil
}

// Enqueue 只加入玩家一次；人数不足时返回空 Match，足够时移除并返回最早的一组。
func (m *Matchmaker) Enqueue(player PlayerID) (Match, error) {
	if player == "" {
		return Match{}, ErrInvalidPlayerID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.queued[player]; exists {
		return Match{}, ErrPlayerAlreadyQueued
	}

	// “判重、入队、凑队并移除”全部在同一临界区完成，确保两个并发请求不会把同一玩家
	// 分进两局，也不会让取消操作看到一半更新的结构。
	m.queued[player] = m.waiting.PushBack(player)
	if m.waiting.Len() < m.playersPerMatch {
		return Match{}, nil
	}

	players := make([]PlayerID, 0, m.playersPerMatch)
	for len(players) < m.playersPerMatch {
		front := m.waiting.Front()
		queuedPlayer := front.Value.(PlayerID)
		players = append(players, queuedPlayer)
		delete(m.queued, queuedPlayer)
		m.waiting.Remove(front)
	}

	return Match{Players: players}, nil
}

// Cancel 移除等待玩家；玩家已匹配或从未排队时返回 false。
func (m *Matchmaker) Cancel(player PlayerID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	element, exists := m.queued[player]
	if !exists {
		return false
	}

	delete(m.queued, player)
	m.waiting.Remove(element)
	return true
}

// Waiting 返回当前排队人数。
func (m *Matchmaker) Waiting() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.waiting.Len()
}
