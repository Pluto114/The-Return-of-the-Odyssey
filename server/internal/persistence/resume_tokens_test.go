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
	if err := store.IssueRoute(context.Background(), "token-1", ResumeRoute{}); !errors.Is(err, ErrInvalidResumeRoute) {
		t.Fatalf("invalid route error = %v, want %v", err, ErrInvalidResumeRoute)
	}
	if err := store.Revoke(context.Background(), ""); !errors.Is(err, ErrInvalidResumeToken) {
		t.Fatalf("invalid revoke error = %v, want %v", err, ErrInvalidResumeToken)
	}
}

func TestResumeTokenKeyDoesNotExposeBearerToken(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })
	store, err := NewResumeTokenStore(client, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := "sensitive-bearer-token"
	key := store.key(token)
	if strings.Contains(key, token) || !strings.HasPrefix(key, resumeTokenPrefix) || len(key) != len(resumeTokenPrefix)+64 {
		t.Fatalf("unsafe or malformed Redis key %q", key)
	}
	if key != store.key(token) || key == store.key(token+"-other") {
		t.Fatal("token key hashing is not deterministic and collision-separated")
	}
}

func TestResumeRouteValidation(t *testing.T) {
	valid := ResumeRoute{Version: ResumeRouteVersion, SessionID: 1, PlayerID: 2, RoomID: 3, Generation: 4}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ResumeRoute){
		func(route *ResumeRoute) { route.Version++ },
		func(route *ResumeRoute) { route.SessionID = 0 },
		func(route *ResumeRoute) { route.PlayerID = 0 },
		func(route *ResumeRoute) { route.RoomID = 0 },
		func(route *ResumeRoute) { route.Generation = 0 },
	} {
		route := valid
		mutate(&route)
		if err := route.Validate(); !errors.Is(err, ErrInvalidResumeRoute) {
			t.Errorf("route %+v error = %v", route, err)
		}
	}
}
