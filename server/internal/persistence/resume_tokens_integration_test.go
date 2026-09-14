//go:build integration

package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestResumeTokenStoreRedisIntegration(t *testing.T) {
	if os.Getenv("ODYSSEY_REDIS_INTEGRATION") != "1" {
		t.Skip("set ODYSSEY_REDIS_INTEGRATION=1 to run against local Redis")
	}

	address := os.Getenv("ODYSSEY_REDIS_ADDR")
	if address == "" {
		address = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{
		Addr:     address,
		Password: os.Getenv("ODYSSEY_REDIS_PASSWORD"),
		DB:       0,
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis at %s is unavailable: %v", address, err)
	}

	service, err := OpenResumeService(ctx, ResumeServiceOptions{
		Addr: address, Password: os.Getenv("ODYSSEY_REDIS_PASSWORD"), TokenTTL: 200 * time.Millisecond, OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	store := service.Store()
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	token := "integration-token-" + unique
	expiringToken := "integration-expiring-" + unique
	routeToken := "integration-route-" + unique
	revokedToken := "integration-revoked-" + unique
	corruptToken := "integration-corrupt-" + unique
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = client.Del(cleanupCtx, store.key(token), store.key(expiringToken), store.key(routeToken), store.key(revokedToken), store.key(corruptToken)).Err()
	})

	if err := store.Issue(ctx, token, "session-1"); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := store.Issue(ctx, token, "session-overwrite"); !errors.Is(err, ErrResumeTokenExists) {
		t.Fatalf("duplicate Issue() error = %v, want %v", err, ErrResumeTokenExists)
	}

	sessionKey, err := store.Consume(ctx, token)
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if sessionKey != "session-1" {
		t.Fatalf("Consume() = %q, want %q", sessionKey, "session-1")
	}
	if _, err := store.Consume(ctx, token); !errors.Is(err, ErrResumeTokenNotFound) {
		t.Fatalf("second Consume() error = %v, want %v", err, ErrResumeTokenNotFound)
	}

	if err := store.Issue(ctx, expiringToken, "session-expiring"); err != nil {
		t.Fatalf("expiring Issue() error = %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := store.Consume(ctx, expiringToken); !errors.Is(err, ErrResumeTokenNotFound) {
		t.Fatalf("expired Consume() error = %v, want %v", err, ErrResumeTokenNotFound)
	}

	route := ResumeRoute{Version: ResumeRouteVersion, SessionID: 11, PlayerID: 22, RoomID: 33, Generation: 44}
	if err := store.IssueRoute(ctx, routeToken, route); err != nil {
		t.Fatalf("IssueRoute() error = %v", err)
	}
	consumedRoute, err := store.ConsumeRoute(ctx, routeToken)
	if err != nil || consumedRoute != route {
		t.Fatalf("ConsumeRoute() = %+v, %v; want %+v", consumedRoute, err, route)
	}
	if _, err := store.ConsumeRoute(ctx, routeToken); !errors.Is(err, ErrResumeTokenNotFound) {
		t.Fatalf("second ConsumeRoute() error = %v", err)
	}

	if err := store.IssueRoute(ctx, revokedToken, route); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, revokedToken); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, revokedToken); err != nil {
		t.Fatalf("second Revoke() should be idempotent: %v", err)
	}
	if _, err := store.ConsumeRoute(ctx, revokedToken); !errors.Is(err, ErrResumeTokenNotFound) {
		t.Fatalf("revoked token error = %v", err)
	}

	if err := client.Set(ctx, store.key(corruptToken), "not-json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeRoute(ctx, corruptToken); !errors.Is(err, ErrCorruptResumeRoute) {
		t.Fatalf("corrupt route error = %v, want %v", err, ErrCorruptResumeRoute)
	}
	if exists, err := client.Exists(ctx, store.key(corruptToken)).Result(); err != nil || exists != 0 {
		t.Fatalf("corrupt consumed route remained replayable: exists=%d err=%v", exists, err)
	}
}
