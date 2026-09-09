// Package config loads the gameserver configuration from environment
// variables and an optional .env file.
//
// This loader exists because server/configs/.env was previously a dead file
// with no reader (PHASE1 §9 calls this out explicitly). It supports the
// ODYSSEY_* keys documented in configs/.env.example.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds the runtime configuration for the gameserver. Field names map
// 1:1 to the ODYSSEY_* environment keys in configs/.env.example.
type Config struct {
	Env         string // development | production
	TCPAddr     string
	AdminAddr   string
	MetricsAddr string
	PprofAddr   string
	TickHz      int
	SnapshotHz  int
	AIHz        int
	LogLevel    string
	MySQLDSN    string
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	// LoginTimeoutSec is how long a connected socket may wait before sending
	// LoginRequest (default 5s). Not in .env.example yet; exposed as a knob.
	LoginTimeoutSec int
}

// Default returns the built-in defaults. These match configs/.env.example so
// the server is runnable even with no .env file present.
func Default() *Config {
	return &Config{
		Env:             "development",
		TCPAddr:         "127.0.0.1:7777",
		AdminAddr:       "127.0.0.1:8080",
		MetricsAddr:     "0.0.0.0:19091", // 9091 is reserved on the integration host
		PprofAddr:       "127.0.0.1:6060",
		TickHz:          30,
		SnapshotHz:      10,
		AIHz:            10,
		LogLevel:        "debug",
		MySQLDSN:        "odyssey:odyssey_local_only@tcp(127.0.0.1:13306)/odyssey?parseTime=true&charset=utf8mb4&loc=UTC",
		RedisAddr:       "127.0.0.1:6379",
		RedisPass:       "odyssey_redis_local_only",
		RedisDB:         0,
		LoginTimeoutSec: 5,
	}
}

// Load reads envPath (a .env file) if it exists, applies those values over the
// defaults, then overlays any ODYSSEY_* variables already set in the process
// environment (env vars win over the file). envPath may be empty to skip the
// file entirely.
func Load(envPath string) (*Config, error) {
	cfg := Default()

	if envPath != "" {
		if err := loadDotEnv(envPath, func(k, v string) {
			// Only apply a key if it is not already present in the process
			// environment; real env vars take precedence.
			if _, exists := os.LookupEnv(k); exists {
				return
			}
			applyKey(cfg, k, v)
		}); err != nil {
			return nil, err
		}
	}

	// Overlay the process environment last.
	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.HasPrefix(kv[0], "ODYSSEY_") {
			applyKey(cfg, kv[0], kv[1])
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate enforces the invariants the server relies on.
func (c *Config) Validate() error {
	if c.TickHz <= 0 {
		return fmt.Errorf("config: ODYSSEY_TICK_HZ must be positive, got %d", c.TickHz)
	}
	if c.SnapshotHz <= 0 || c.SnapshotHz > c.TickHz {
		return fmt.Errorf("config: ODYSSEY_SNAPSHOT_HZ must be in (0, %d], got %d", c.TickHz, c.SnapshotHz)
	}
	if c.TCPAddr == "" {
		return fmt.Errorf("config: ODYSSEY_TCP_ADDR must not be empty")
	}
	return nil
}

// applyKey writes a single ODYSSEY_* key/value into cfg. Unknown keys are
// ignored (forward-compatible with future config additions).
func applyKey(cfg *Config, key, val string) {
	switch key {
	case "ODYSSEY_ENV":
		cfg.Env = val
	case "ODYSSEY_TCP_ADDR":
		cfg.TCPAddr = val
	case "ODYSSEY_ADMIN_ADDR":
		cfg.AdminAddr = val
	case "ODYSSEY_METRICS_ADDR":
		cfg.MetricsAddr = val
	case "ODYSSEY_PPROF_ADDR":
		cfg.PprofAddr = val
	case "ODYSSEY_TICK_HZ":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TickHz = n
		}
	case "ODYSSEY_SNAPSHOT_HZ":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SnapshotHz = n
		}
	case "ODYSSEY_AI_HZ":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.AIHz = n
		}
	case "ODYSSEY_LOG_LEVEL":
		cfg.LogLevel = val
	case "ODYSSEY_MYSQL_DSN":
		cfg.MySQLDSN = val
	case "ODYSSEY_REDIS_ADDR":
		cfg.RedisAddr = val
	case "ODYSSEY_REDIS_PASSWORD":
		cfg.RedisPass = val
	case "ODYSSEY_REDIS_DB":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.RedisDB = n
		}
	case "ODYSSEY_LOGIN_TIMEOUT_SEC":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.LoginTimeoutSec = n
		}
	}
}

// loadDotEnv parses a simple KEY=VALUE .env file. It supports blank lines and
// full-line `#` comments; inline comments are NOT supported (values may
// legitimately contain `#`, e.g. passwords). Values are not shell-unquoted in
// v1; write them bare.
func loadDotEnv(path string, apply func(k, v string)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no .env is fine; defaults + env vars apply
		}
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		apply(k, v)
	}
	return sc.Err()
}
