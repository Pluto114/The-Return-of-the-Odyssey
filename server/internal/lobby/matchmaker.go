// Package lobby owns matchmaking state and policy.
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

// PlayerID is the stable player identity supplied by the session module.
// Lobby treats it as opaque and does not normalize it.
type PlayerID string

// Match is a complete FIFO group ready for the room module to accept.
// Players is owned by the caller and may be modified after Enqueue returns.
type Match struct {
	Players []PlayerID
}

// Found reports whether Enqueue formed a complete match.
func (m Match) Found() bool {
	return len(m.Players) != 0
}

// Matchmaker groups unique players in FIFO order. Its methods are safe for
// concurrent use. It deliberately does not depend on protocol, session, or room
// packages: callers translate network requests into PlayerIDs and hand completed
// matches to the room owner.
type Matchmaker struct {
	mu              sync.Mutex
	playersPerMatch int
	waiting         *list.List
	queued          map[PlayerID]*list.Element
}

// NewMatchmaker creates an empty matcher. A match size of zero or less is
// rejected because it cannot produce a meaningful group.
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

// Enqueue adds a player once. It returns an empty Match while the queue is
// incomplete, or removes and returns the oldest complete group.
func (m *Matchmaker) Enqueue(player PlayerID) (Match, error) {
	if player == "" {
		return Match{}, ErrInvalidPlayerID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.queued[player]; exists {
		return Match{}, ErrPlayerAlreadyQueued
	}

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

// Cancel removes a waiting player. It returns false after the player has
// already matched or when the player was never queued.
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

// Waiting returns the number of players currently queued.
func (m *Matchmaker) Waiting() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.waiting.Len()
}
