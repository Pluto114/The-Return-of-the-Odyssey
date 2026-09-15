package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	resumeTokenPrefix   = "odyssey:resume:v1:"
	maxResumeTokenBytes = 256
	maxSessionKeyBytes  = 256
)

var (
	ErrInvalidRedisClient  = errors.New("redis client must not be nil")
	ErrInvalidResumeTTL    = errors.New("resume token TTL must be positive")
	ErrInvalidResumeToken  = errors.New("resume token must contain 1 to 256 bytes")
	ErrInvalidSessionKey   = errors.New("session key must contain 1 to 256 bytes")
	ErrResumeTokenExists   = errors.New("resume token already exists")
	ErrResumeTokenNotFound = errors.New("resume token not found")
	ErrInvalidResumeRoute  = errors.New("invalid resume route")
	ErrCorruptResumeRoute  = errors.New("corrupt resume route")
	ErrResumeBackend       = errors.New("resume token backend unavailable")
)

const ResumeRouteVersion = 1

// ResumeRoute is the minimum durable binding needed to find the live Session
// and Room after a reconnect. It never contains a World snapshot.
type ResumeRoute struct {
	Version    uint8  `json:"version"`
	SessionID  uint64 `json:"session_id"`
	PlayerID   uint64 `json:"player_id"`
	RoomID     uint64 `json:"room_id"`
	Generation uint64 `json:"generation"`
}

func (r ResumeRoute) Validate() error {
	if r.Version != ResumeRouteVersion || r.SessionID == 0 || r.PlayerID == 0 || r.RoomID == 0 || r.Generation == 0 {
		return ErrInvalidResumeRoute
	}
	return nil
}

// ResumeTokenStore persists one-time resume tokens in Redis. Tokens and
// session keys are opaque; the session module owns their format and meaning.
type ResumeTokenStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewResumeTokenStore creates a Redis-backed token store. The TTL is applied
// by Redis when Issue succeeds, so abandoned tokens expire without cleanup by
// the gameserver process.
func NewResumeTokenStore(client *redis.Client, ttl time.Duration) (*ResumeTokenStore, error) {
	if client == nil {
		return nil, ErrInvalidRedisClient
	}
	if ttl <= 0 {
		return nil, ErrInvalidResumeTTL
	}

	return &ResumeTokenStore{client: client, ttl: ttl}, nil
}

// Issue associates a new token with a session key until the configured TTL.
// Existing tokens are never overwritten.
func (s *ResumeTokenStore) Issue(ctx context.Context, token, sessionKey string) error {
	if !validOpaqueValue(token, maxResumeTokenBytes) {
		return ErrInvalidResumeToken
	}
	if !validOpaqueValue(sessionKey, maxSessionKeyBytes) {
		return ErrInvalidSessionKey
	}

	stored, err := s.client.SetNX(ctx, s.key(token), sessionKey, s.ttl).Result()
	if err != nil {
		return backendError("issue", err)
	}
	if !stored {
		return ErrResumeTokenExists
	}
	return nil
}

// Consume atomically returns and deletes a token's session key. Concurrent or
// repeated consumers therefore cannot resume the same token twice.
func (s *ResumeTokenStore) Consume(ctx context.Context, token string) (string, error) {
	if !validOpaqueValue(token, maxResumeTokenBytes) {
		return "", ErrInvalidResumeToken
	}

	sessionKey, err := s.client.GetDel(ctx, s.key(token)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrResumeTokenNotFound
	}
	if err != nil {
		return "", backendError("consume", err)
	}
	return sessionKey, nil
}

// IssueRoute stores a validated, versioned Session/Player/Room binding using
// the same SETNX+TTL semantics as Issue.
func (s *ResumeTokenStore) IssueRoute(ctx context.Context, token string, route ResumeRoute) error {
	if err := route.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("encode resume route: %w", err)
	}
	return s.Issue(ctx, token, string(payload))
}

// ConsumeRoute atomically consumes a token before decoding its route. A
// corrupt backend value cannot be replayed after it is detected.
func (s *ResumeTokenStore) ConsumeRoute(ctx context.Context, token string) (ResumeRoute, error) {
	payload, err := s.Consume(ctx, token)
	if err != nil {
		return ResumeRoute{}, err
	}
	var route ResumeRoute
	if err := json.Unmarshal([]byte(payload), &route); err != nil {
		return ResumeRoute{}, fmt.Errorf("%w: decode: %v", ErrCorruptResumeRoute, err)
	}
	if err := route.Validate(); err != nil {
		return ResumeRoute{}, fmt.Errorf("%w: %v", ErrCorruptResumeRoute, err)
	}
	return route, nil
}

// Revoke removes a token during permanent leave or logout. It is idempotent;
// absence is already the desired state.
func (s *ResumeTokenStore) Revoke(ctx context.Context, token string) error {
	if !validOpaqueValue(token, maxResumeTokenBytes) {
		return ErrInvalidResumeToken
	}
	if err := s.client.Del(ctx, s.key(token)).Err(); err != nil {
		return backendError("revoke", err)
	}
	return nil
}

func (s *ResumeTokenStore) key(token string) string {
	digest := sha256.Sum256([]byte(token))
	return resumeTokenPrefix + hex.EncodeToString(digest[:])
}

func validOpaqueValue(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes
}

func backendError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrResumeBackend, operation, err)
}
