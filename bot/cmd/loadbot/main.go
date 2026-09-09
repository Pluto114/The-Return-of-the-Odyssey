package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	botclient "github.com/Pluto114/The-Return-of-the-Odyssey/bot/internal/client"
	"github.com/Pluto114/The-Return-of-the-Odyssey/bot/internal/load"
)

func main() {
	address := flag.String("server", "127.0.0.1:7777", "gameserver TCP address")
	clients := flag.Int("clients", 10, "number of concurrent bots")
	duration := flag.Duration("duration", 10*time.Minute, "total run duration")
	ramp := flag.Duration("ramp", time.Second, "time between first and last bot start")
	flag.Parse()

	report, err := load.Run(context.Background(), load.Config{
		Clients: *clients, RampUp: *ramp, Duration: *duration,
	}, func(ctx context.Context, clientID int) error {
		return botclient.Run(ctx, *address, clientID)
	})
	result := struct {
		Planned   int    `json:"planned"`
		Started   int    `json:"started"`
		Succeeded int    `json:"succeeded"`
		Failed    int    `json:"failed"`
		Elapsed   string `json:"elapsed"`
		Server    string `json:"server"`
		OS        string `json:"os"`
		Arch      string `json:"arch"`
		Go        string `json:"go_version"`
	}{
		Planned: report.Planned, Started: report.Started, Succeeded: report.Succeeded,
		Failed: report.Failed, Elapsed: report.Elapsed.String(), Server: *address,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(),
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
