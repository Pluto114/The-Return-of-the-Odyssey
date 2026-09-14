package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// This file owns the process-local session registry: the in-memory mapping that
// turns a one-time resume token back into the original *Session after a
// transient disconnect.
//
// Ownership boundary (D8): the durable Redis token store belongs to Role D
// (internal/persistence.ResumeTokenStore). This registry is Role A's
// process-local view — it is the authoritative place that binds a token to the
// live *Session object, because only A holds the connection/session lifecycle.
// It intentionally does NOT import persistence or network; the caller wires it
// into the login/resume handlers.

var (
	// ErrTokenNotFound is returned when a resume token is unknown or already
	// consumed (tokens are single-use).
	ErrTokenNotFound = errors.New("resume token not found")
	// ErrTokenExpired is returned when a token is known but its grace window
	// has elapsed.
	ErrTokenExpired = errors.New("resume token expired")
	// ErrSessionActive is returned when a resume targets a session whose
	// connection is still alive (a resume must follow a disconnect).
	ErrSessionActive = errors.New("session is still connected")
)

// RegistryEntry is the live binding the registry keeps for one session while a
// resume is possible. It is mutated only under the registry lock.
type registryEntry struct {
	session   *Session
	token     string
	expiresAt time.Time
	// disconnected marks that the session's transport dropped and the grace
	// window is running. While false, the token is only issued to be used
	// later and resume is not yet permitted.
	disconnected bool
}

// Registry maps one-time resume tokens to their live *Session and enforces the
// reconnect grace window. It is safe for concurrent use.
type Registry struct {
	mu        sync.Mutex
	ttl       time.Duration
	byToken   map[string]*registryEntry
	bySession map[uint64]*registryEntry
}

// NewRegistry returns a registry whose resume tokens are valid for ttl after
// issue. ttl must be positive.
func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = time.Second
	}
	return &Registry{
		ttl:       ttl,
		byToken:   make(map[string]*registryEntry),
		bySession: make(map[uint64]*registryEntry),
	}
}

// Issue creates a single-use resume token bound to sess and returns it. The
// previous token for the same session (if any) is revoked first, so a session
// never has two live tokens. The token must be handed to the client in the
// LoginResponse; it becomes consumable only after the session disconnects.
func (r *Registry) Issue(sess *Session) (string, error) {
	if sess == nil {
		return "", errors.New("session must not be nil")
	}
	sessionID, _ := sess.Identity()
	if sessionID == 0 {
		return "", errors.New("session has no identity")
	}

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokeLocked(sessionID)
	entry := &registryEntry{
		session:   sess,
		token:     token,
		expiresAt: time.Now().Add(r.ttl),
	}
	r.byToken[token] = entry
	r.bySession[sessionID] = entry
	return token, nil
}

// MarkDisconnected records that sess's transport dropped and starts (or
// refreshes) its grace window. Resume is only permitted for a session that has
// been marked disconnected. It is a no-op if the session is not registered.
func (r *Registry) MarkDisconnected(sess *Session) {
	if sess == nil {
		return
	}
	sessionID, _ := sess.Identity()
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.bySession[sessionID]
	if !ok {
		return
	}
	entry.disconnected = true
	entry.expiresAt = time.Now().Add(r.ttl)
}

// Resolve consumes a token and returns the session it was issued for, enforcing
// the single-use and grace-window rules. On success the token is removed so a
// second consume cannot succeed.
//
// It reports ErrTokenNotFound for an unknown or already-consumed token, and
// ErrTokenExpired when the token's grace window has elapsed. A token that is
// valid but whose session has not yet disconnected returns ErrSessionActive —
// the caller must not rebind a still-live session.
func (r *Registry) Resolve(token string) (*Session, error) {
	if token == "" {
		return nil, ErrTokenNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.byToken[token]
	if !ok {
		return nil, ErrTokenNotFound
	}
	if time.Now().After(entry.expiresAt) {
		r.revokeEntryLocked(entry)
		return nil, ErrTokenExpired
	}
	if !entry.disconnected {
		return nil, ErrSessionActive
	}
	r.revokeEntryLocked(entry)
	return entry.session, nil
}

// Revoke invalidates any live token for sess. It is used when a session closes
// permanently (grace expired, room removed) so a stale token cannot be reused.
func (r *Registry) Revoke(sess *Session) {
	if sess == nil {
		return
	}
	sessionID, _ := sess.Identity()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokeLocked(sessionID)
}

// Active reports whether the session currently holds a live (unconsumed,
// unexpired) resume token. It is a cheap health check, not a consume.
func (r *Registry) Active(sess *Session) bool {
	if sess == nil {
		return false
	}
	sessionID, _ := sess.Identity()
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.bySession[sessionID]
	if !ok {
		return false
	}
	return time.Now().Before(entry.expiresAt)
}

func (r *Registry) revokeLocked(sessionID uint64) {
	if entry, ok := r.bySession[sessionID]; ok {
		r.revokeEntryLocked(entry)
	}
}

func (r *Registry) revokeEntryLocked(entry *registryEntry) {
	sessionID, _ := entry.session.Identity()
	delete(r.byToken, entry.token)
	delete(r.bySession, sessionID)
}
