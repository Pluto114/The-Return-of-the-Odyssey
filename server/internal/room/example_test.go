package room_test

import (
	"context"
	"fmt"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// This example is compiled and executed by go test; it uses the public seam
// intended for A's session adapter and D's lobby, without a network server.
func ExampleStart() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := room.Start(ctx, 1, room.DefaultConfig())
	if err != nil {
		panic(err)
	}
	defer r.Close()
	joined, err := r.Join(11, 101) // Both IDs come from trusted server state.
	if err != nil {
		panic(err)
	}
	if err := <-joined; err != nil {
		panic(err)
	}
	if err := r.Input(11, game.Input{Seq: 1, Direction: entity.Vec2{X: 1}}); err != nil {
		panic(err)
	}
	for s := range r.Snapshots() {
		if len(s.Players) == 1 && s.Players[0].LastProcessedInputSeq == 1 {
			fmt.Println(s.RoomID, s.Players[0].ID, s.Players[0].LastProcessedInputSeq)
			return
		}
	}
	panic("room closed before input acknowledgement")
	// Output: 1 101 1
}
