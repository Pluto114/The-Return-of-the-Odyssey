package persistence

import (
	"context"
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
)

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
		return fmt.Errorf("issue resume token: %w", err)
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
		return "", fmt.Errorf("consume resume token: %w", err)
	}
	return sessionKey, nil
}

func (s *ResumeTokenStore) key(token string) string {
	return resumeTokenPrefix + token
}

func validOpaqueValue(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes
}
