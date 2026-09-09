package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNewResumeTokenStoreValidatesDependencies(t *testing.T) {
	t.Parallel()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })

	if _, err := NewResumeTokenStore(nil, time.Minute); !errors.Is(err, ErrInvalidRedisClient) {
		t.Fatalf("nil client error = %v, want %v", err, ErrInvalidRedisClient)
	}
	for _, ttl := range []time.Duration{-time.Second, 0} {
		if _, err := NewResumeTokenStore(client, ttl); !errors.Is(err, ErrInvalidResumeTTL) {
			t.Fatalf("TTL %s error = %v, want %v", ttl, err, ErrInvalidResumeTTL)
		}
	}
	if _, err := NewResumeTokenStore(client, time.Minute); err != nil {
		t.Fatalf("valid constructor error = %v", err)
	}
}

func TestResumeTokenStoreRejectsInvalidValuesBeforeRedis(t *testing.T) {
	t.Parallel()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })
	store, err := NewResumeTokenStore(client, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	for _, token := range []string{"", strings.Repeat("t", maxResumeTokenBytes+1)} {
		if err := store.Issue(context.Background(), token, "session-1"); !errors.Is(err, ErrInvalidResumeToken) {
			t.Fatalf("Issue token length %d error = %v, want %v", len(token), err, ErrInvalidResumeToken)
		}
		if _, err := store.Consume(context.Background(), token); !errors.Is(err, ErrInvalidResumeToken) {
			t.Fatalf("Consume token length %d error = %v, want %v", len(token), err, ErrInvalidResumeToken)
		}
	}

	for _, sessionKey := range []string{"", strings.Repeat("s", maxSessionKeyBytes+1)} {
		if err := store.Issue(context.Background(), "token-1", sessionKey); !errors.Is(err, ErrInvalidSessionKey) {
			t.Fatalf("Issue session key length %d error = %v, want %v", len(sessionKey), err, ErrInvalidSessionKey)
		}
	}
}
