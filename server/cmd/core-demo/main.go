// core-demo runs deterministic, headless B-module combat. It does not start a
// network server or represent a real client/Bot integration or load test.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func run() error {
	config := game.DefaultConfig()
	// The fixed-position offline shooter exercises combat math and stage
	// completion; arena cover is exercised by the game arena tests.
	config.CoverEnabled = false
	w, err := game.NewWorld(config)
	if err != nil {
		return err
	}
	if err = w.AddPlayer(1); err != nil {
		return err
	}
	plan, err := game.NewFirstStagePlan(config, 42)
	if err != nil {
		return err
	}
	if err = w.StartStage(plan); err != nil {
		return err
	}
	counts := map[string]int{}
	for seq := uint32(1); seq <= 300; seq++ {
		s := w.Snapshot()
		if s.Stage.State != stage.Playing {
			break
		}
		input := game.Input{Seq: seq}
		if len(s.Monsters) > 0 {
			input.Shoot = true
			input.Aim = entity.Vec2{X: s.Monsters[0].Position.X - s.Players[0].Position.X, Y: s.Monsters[0].Position.Y - s.Players[0].Position.Y}
		}
		now := time.Unix(100, 0).Add(time.Duration(seq-1) * game.TickInterval)
		if err = w.ApplyInput(1, input, now); err != nil {
			return err
		}
		w.Step(now)
		batch := w.TakeEvents()
		if batch.Overflow {
			return fmt.Errorf("event overflow")
		}
		for _, event := range batch.Events {
			switch event.Kind {
			case game.ProjectileSpawned:
				counts["shots"]++
			case game.DamageDealt:
				counts["hits"]++
			case game.EntityDied:
				counts["deaths"]++
			}
		}
	}
	s := w.Snapshot()
	result := struct {
		Mode, Result      string
		Tick              uint64
		PlayerHealth      float64
		MonstersRemaining int
		Events            map[string]int
	}{
		Mode: "offline_fixed_tick", Result: "stage_clear", Tick: s.ServerTick, PlayerHealth: s.Players[0].Health, MonstersRemaining: len(s.Monsters), Events: counts}
	if s.Stage.State != stage.StageClear {
		result.Result = "not_cleared"
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if s.Stage.State != stage.StageClear {
		return fmt.Errorf("stage did not clear within 300 ticks")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
