package session

import (
	"testing"
	"time"
)

func TestRegistryIssueAndResolve(t *testing.T) {
	r := NewRegistry(time.Minute)
	s := New()
	s.AssignIdentity(7, 11)

	token, err := r.Issue(s)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("Issue returned an empty token")
	}
	if !r.Active(s) {
		t.Fatal("expected session to hold an active token after Issue")
	}

	// A token cannot be consumed before the session disconnects.
	if _, err := r.Resolve(token); err != ErrSessionActive {
		t.Fatalf("Resolve before disconnect = %v, want ErrSessionActive", err)
	}

	r.MarkDisconnected(s)
	got, err := r.Resolve(token)
	if err != nil {
		t.Fatalf("Resolve after disconnect: %v", err)
	}
	if got != s {
		t.Fatalf("Resolve returned %p, want %p", got, s)
	}

	// Single-use: a second consume must fail.
	if _, err := r.Resolve(token); err != ErrTokenNotFound {
		t.Fatalf("second Resolve = %v, want ErrTokenNotFound", err)
	}
}

func TestRegistryTokenNotFound(t *testing.T) {
	r := NewRegistry(time.Minute)
	if _, err := r.Resolve("deadbeef"); err != ErrTokenNotFound {
		t.Fatalf("Resolve unknown token = %v, want ErrTokenNotFound", err)
	}
	if _, err := r.Resolve(""); err != ErrTokenNotFound {
		t.Fatalf("Resolve empty token = %v, want ErrTokenNotFound", err)
	}
}

func TestRegistryTokenExpired(t *testing.T) {
	r := NewRegistry(time.Millisecond)
	s := New()
	s.AssignIdentity(1, 1)

	token, err := r.Issue(s)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	r.MarkDisconnected(s)

	time.Sleep(5 * time.Millisecond)
	if _, err := r.Resolve(token); err != ErrTokenExpired {
		t.Fatalf("Resolve expired = %v, want ErrTokenExpired", err)
	}
	if r.Active(s) {
		t.Fatal("expired token should no longer be active")
	}
}

func TestRegistryIssueReplacesPrevious(t *testing.T) {
	r := NewRegistry(time.Minute)
	s := New()
	s.AssignIdentity(2, 2)

	first, err := r.Issue(s)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, err := r.Issue(s)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if first == second {
		t.Fatal("expected a new token on re-issue")
	}

	// The first token must be revoked by the re-issue.
	r.MarkDisconnected(s)
	if _, err := r.Resolve(first); err != ErrTokenNotFound {
		t.Fatalf("Resolve revoked token = %v, want ErrTokenNotFound", err)
	}
	if _, err := r.Resolve(second); err != nil {
		t.Fatalf("Resolve current token: %v", err)
	}
}

func TestRegistryRevoke(t *testing.T) {
	r := NewRegistry(time.Minute)
	s := New()
	s.AssignIdentity(3, 3)

	token, err := r.Issue(s)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	r.Revoke(s)
	r.MarkDisconnected(s)
	if _, err := r.Resolve(token); err != ErrTokenNotFound {
		t.Fatalf("Resolve revoked token = %v, want ErrTokenNotFound", err)
	}
	if r.Active(s) {
		t.Fatal("revoked session should not be active")
	}
}
