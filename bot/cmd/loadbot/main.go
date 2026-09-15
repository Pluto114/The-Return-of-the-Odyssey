package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	botclient "github.com/Pluto114/The-Return-of-the-Odyssey/bot/internal/client"
	"github.com/Pluto114/The-Return-of-the-Odyssey/bot/internal/load"
)

const (
	modeFunctional = "functional"
	modeSustained  = "sustained"
)

type lifecycleSummary struct {
	mu sync.Mutex

	MatchesCompleted  uint64
	StagesCleared     uint64
	RewardsApplied    uint64
	ReadySent         uint64
	PotionInputs      uint64
	RecoveryAttempts  uint64
	RecoverySucceeded uint64
	PhaseSucceeded    map[string]uint64
	PhaseFailed       map[string]uint64
	LastProgress      botclient.MatchResult
	FirstFailure      string
}

type matchRunner func(context.Context, int) (botclient.MatchResult, error)

func newLifecycleSummary() *lifecycleSummary {
	return &lifecycleSummary{PhaseSucceeded: make(map[string]uint64), PhaseFailed: make(map[string]uint64)}
}

func (s *lifecycleSummary) record(result botclient.MatchResult, runErr error, expectedShutdown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastProgress = result
	s.StagesCleared += uint64(result.StagesCleared)
	s.RewardsApplied += uint64(result.RewardsApplied)
	s.ReadySent += uint64(result.ReadySent)
	s.PotionInputs += uint64(result.PotionInputs)
	s.RecoveryAttempts += uint64(result.RecoveryAttempts)
	s.RecoverySucceeded += uint64(result.RecoverySucceeded)
	if result.SessionID != 0 {
		s.PhaseSucceeded[string(botclient.PhaseLogin)]++
	}
	if result.RoomID != 0 {
		s.PhaseSucceeded[string(botclient.PhaseMatch)]++
	}
	s.PhaseSucceeded[string(botclient.PhaseCombat)] += uint64(result.StagesCleared)
	s.PhaseSucceeded[string(botclient.PhaseReward)] += uint64(result.RewardsApplied)
	s.PhaseSucceeded[string(botclient.PhaseReady)] += uint64(result.ReadySent)
	s.PhaseSucceeded[string(botclient.PhaseResume)] += uint64(result.RecoverySucceeded)
	if runErr == nil && result.Phase == botclient.PhaseComplete {
		s.MatchesCompleted++
		s.PhaseSucceeded[string(botclient.PhaseComplete)]++
	} else if runErr != nil && !expectedShutdown {
		s.PhaseFailed[string(result.Phase)]++
		if s.FirstFailure == "" {
			s.FirstFailure = runErr.Error()
		}
	}
}

func workerForMode(mode string, runMatch matchRunner, summary *lifecycleSummary) load.Worker {
	return func(ctx context.Context, clientID int) error {
		if mode == modeFunctional {
			result, runErr := runMatch(ctx, clientID)
			summary.record(result, runErr, false)
			return runErr
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, runErr := runMatch(ctx, clientID)
			expectedShutdown := ctx.Err() != nil
			summary.record(result, runErr, expectedShutdown)
			if expectedShutdown {
				return ctx.Err()
			}
			if runErr != nil {
				return runErr
			}
		}
	}
}

type output struct {
	Planned           int                      `json:"planned"`
	Started           int                      `json:"started"`
	Succeeded         int                      `json:"succeeded"`
	Failed            int                      `json:"failed"`
	Elapsed           string                   `json:"elapsed"`
	Server            string                   `json:"server"`
	Mode              string                   `json:"mode"`
	TargetStages      uint32                   `json:"target_stages"`
	RewardScenario    botclient.RewardScenario `json:"reward_scenario"`
	Seed              uint64                   `json:"seed"`
	MatchesCompleted  uint64                   `json:"matches_completed"`
	StagesCleared     uint64                   `json:"stages_cleared"`
	RewardsApplied    uint64                   `json:"rewards_applied"`
	ReadySent         uint64                   `json:"ready_sent"`
	PotionInputs      uint64                   `json:"potion_inputs"`
	RecoveryAttempts  uint64                   `json:"recovery_attempts"`
	RecoverySucceeded uint64                   `json:"recovery_succeeded"`
	PhaseSucceeded    map[string]uint64        `json:"phase_succeeded"`
	PhaseFailed       map[string]uint64        `json:"phase_failed"`
	LastProgress      botclient.MatchResult    `json:"last_progress"`
	FirstFailure      string                   `json:"first_failure,omitempty"`
	OS                string                   `json:"os"`
	Arch              string                   `json:"arch"`
	Go                string                   `json:"go_version"`
}

func main() {
	address := flag.String("server", "127.0.0.1:7777", "gameserver TCP address")
	clients := flag.Int("clients", 10, "number of concurrent bots")
	duration := flag.Duration("duration", 10*time.Minute, "total run duration")
	ramp := flag.Duration("ramp", time.Second, "time between first and last bot start")
	mode := flag.String("mode", modeFunctional, "functional (one match) or sustained (repeat matches until duration)")
	stages := flag.Uint("stages", 3, "authoritative stages required per completed match")
	seed := flag.Uint64("seed", 1, "deterministic reward-selection seed")
	rewardScenario := flag.String("reward-scenario", string(botclient.RewardNormal), "normal, invalid, duplicate, or timeout")
	usePotion := flag.Bool("use-potion", true, "send one potion-use intent after each first stage")
	resume := flag.Bool("resume", true, "attempt bounded reconnect when the server issued a resume token")
	flag.Parse()
	if *mode != modeFunctional && *mode != modeSustained {
		fmt.Fprintf(os.Stderr, "invalid mode %q\n", *mode)
		os.Exit(2)
	}
	if *stages == 0 || *stages > 1000 {
		fmt.Fprintf(os.Stderr, "stages must be in [1, 1000]\n")
		os.Exit(2)
	}

	matchConfig := botclient.DefaultMatchConfig()
	matchConfig.TargetStages = uint32(*stages)
	matchConfig.Seed = *seed
	matchConfig.RewardScenario = botclient.RewardScenario(*rewardScenario)
	matchConfig.UsePotion = *usePotion
	matchConfig.EnableResume = *resume
	if !*resume {
		matchConfig.MaxResumeAttempts = 0
	}
	if err := matchConfig.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	summary := newLifecycleSummary()
	runMatch := func(ctx context.Context, clientID int) (botclient.MatchResult, error) {
		return botclient.RunMatch(ctx, *address, clientID, matchConfig)
	}
	worker := workerForMode(*mode, runMatch, summary)

	report, err := load.Run(context.Background(), load.Config{
		Clients: *clients, RampUp: *ramp, Duration: *duration,
	}, worker)
	summary.mu.Lock()
	result := output{
		Planned: report.Planned, Started: report.Started, Succeeded: report.Succeeded, Failed: report.Failed,
		Elapsed: report.Elapsed.String(), Server: *address, Mode: *mode, TargetStages: matchConfig.TargetStages,
		RewardScenario: matchConfig.RewardScenario, Seed: matchConfig.Seed,
		MatchesCompleted: summary.MatchesCompleted, StagesCleared: summary.StagesCleared,
		RewardsApplied: summary.RewardsApplied, ReadySent: summary.ReadySent, PotionInputs: summary.PotionInputs,
		RecoveryAttempts: summary.RecoveryAttempts, RecoverySucceeded: summary.RecoverySucceeded,
		PhaseSucceeded: summary.PhaseSucceeded, PhaseFailed: summary.PhaseFailed,
		LastProgress: summary.LastProgress, FirstFailure: summary.FirstFailure,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(),
	}
	summary.mu.Unlock()
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
