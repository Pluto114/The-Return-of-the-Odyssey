package lobby

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func TestNewMatchmakerRejectsInvalidSize(t *testing.T) {
	t.Parallel()

	for _, size := range []int{-1, 0} {
		if _, err := NewMatchmaker(size); !errors.Is(err, ErrInvalidPlayersPerMatch) {
			t.Fatalf("NewMatchmaker(%d) error = %v, want %v", size, err, ErrInvalidPlayersPerMatch)
		}
	}
}

func TestMatchmakerFormsFIFOGroup(t *testing.T) {
	t.Parallel()

	matcher, err := NewMatchmaker(3)
	if err != nil {
		t.Fatal(err)
	}

	for _, player := range []PlayerID{"player-1", "player-2"} {
		match, enqueueErr := matcher.Enqueue(player)
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		if match.Found() {
			t.Fatalf("Enqueue(%q) formed an early match: %v", player, match.Players)
		}
	}

	match, err := matcher.Enqueue("player-3")
	if err != nil {
		t.Fatal(err)
	}
	if !match.Found() {
		t.Fatal("third player did not form a match")
	}

	want := []PlayerID{"player-1", "player-2", "player-3"}
	if !slices.Equal(match.Players, want) {
		t.Fatalf("matched players = %v, want %v", match.Players, want)
	}
	if waiting := matcher.Waiting(); waiting != 0 {
		t.Fatalf("Waiting() = %d, want 0", waiting)
	}
}

func TestMatchmakerRejectsInvalidAndDuplicatePlayer(t *testing.T) {
	t.Parallel()

	matcher, err := NewMatchmaker(2)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := matcher.Enqueue(""); !errors.Is(err, ErrInvalidPlayerID) {
		t.Fatalf("empty player error = %v, want %v", err, ErrInvalidPlayerID)
	}
	if _, err := matcher.Enqueue("player-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := matcher.Enqueue("player-1"); !errors.Is(err, ErrPlayerAlreadyQueued) {
		t.Fatalf("duplicate player error = %v, want %v", err, ErrPlayerAlreadyQueued)
	}
	if waiting := matcher.Waiting(); waiting != 1 {
		t.Fatalf("Waiting() = %d, want 1", waiting)
	}
}

func TestMatchmakerCancelPreservesFIFOOrder(t *testing.T) {
	t.Parallel()

	matcher, err := NewMatchmaker(2)
	if err != nil {
		t.Fatal(err)
	}

	for _, player := range []PlayerID{"player-1", "player-2"} {
		if _, err := matcher.Enqueue(player); err != nil {
			t.Fatal(err)
		}
		if player == "player-1" && !matcher.Cancel(player) {
			t.Fatalf("Cancel(%q) = false, want true", player)
		}
	}

	match, err := matcher.Enqueue("player-3")
	if err != nil {
		t.Fatal(err)
	}
	want := []PlayerID{"player-2", "player-3"}
	if !slices.Equal(match.Players, want) {
		t.Fatalf("matched players = %v, want %v", match.Players, want)
	}
	if matcher.Cancel("player-1") {
		t.Fatal("second cancellation succeeded")
	}
	if matcher.Cancel("player-2") {
		t.Fatal("matched player was still cancellable")
	}
}

func TestMatchmakerConcurrentEnqueue(t *testing.T) {
	t.Parallel()

	const (
		playersPerMatch = 4
		playerCount     = 101
	)

	matcher, err := NewMatchmaker(playersPerMatch)
	if err != nil {
		t.Fatal(err)
	}

	matches := make(chan Match, playerCount/playersPerMatch)
	errorsSeen := make(chan error, playerCount)
	var workers sync.WaitGroup
	workers.Add(playerCount)
	for i := 0; i < playerCount; i++ {
		player := PlayerID(fmt.Sprintf("player-%03d", i))
		go func() {
			defer workers.Done()
			match, enqueueErr := matcher.Enqueue(player)
			if enqueueErr != nil {
				errorsSeen <- enqueueErr
				return
			}
			if match.Found() {
				matches <- match
			}
		}()
	}
	workers.Wait()
	close(matches)
	close(errorsSeen)

	for enqueueErr := range errorsSeen {
		t.Errorf("concurrent Enqueue error: %v", enqueueErr)
	}

	seen := make(map[PlayerID]struct{}, playerCount-1)
	matchCount := 0
	for match := range matches {
		matchCount++
		if len(match.Players) != playersPerMatch {
			t.Errorf("match size = %d, want %d", len(match.Players), playersPerMatch)
		}
		for _, player := range match.Players {
			if _, duplicate := seen[player]; duplicate {
				t.Errorf("player %q matched more than once", player)
			}
			seen[player] = struct{}{}
		}
	}

	if want := playerCount / playersPerMatch; matchCount != want {
		t.Errorf("match count = %d, want %d", matchCount, want)
	}
	if want := playerCount - playerCount%playersPerMatch; len(seen) != want {
		t.Errorf("matched player count = %d, want %d", len(seen), want)
	}
	if waiting := matcher.Waiting(); waiting != playerCount%playersPerMatch {
		t.Errorf("Waiting() = %d, want %d", waiting, playerCount%playersPerMatch)
	}
}
