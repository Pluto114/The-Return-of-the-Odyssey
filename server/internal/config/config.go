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
	"math"
	"net"
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

	EquipmentCatalogPath       string
	RewardDurationSec          int
	StageLimit                 int
	FirstStageSeedBase         int64
	DirectorMinDifficulty      float64
	DirectorMaxDifficulty      float64
	DirectorTargetClearTimeSec float64
	DirectorTargetDPS          float64
	DirectorTargetDamageTaken  float64

	// LoginTimeoutSec is how long a connected socket may wait before sending
	// LoginRequest (default 5s). Not in .env.example yet; exposed as a knob.
	LoginTimeoutSec int

	parseErr error
}

// Default returns the built-in defaults. These match configs/.env.example so
// the server is runnable even with no .env file present.
func Default() *Config {
	return &Config{
		Env:                        "development",
		TCPAddr:                    "127.0.0.1:7777",
		AdminAddr:                  "127.0.0.1:8080",
		MetricsAddr:                "0.0.0.0:19091", // 9091 is reserved on the integration host
		PprofAddr:                  "127.0.0.1:6060",
		TickHz:                     30,
		SnapshotHz:                 10,
		AIHz:                       10,
		LogLevel:                   "debug",
		MySQLDSN:                   "odyssey:odyssey_local_only@tcp(127.0.0.1:13306)/odyssey?parseTime=true&charset=utf8mb4&loc=UTC",
		RedisAddr:                  "127.0.0.1:6379",
		RedisPass:                  "odyssey_redis_local_only",
		RedisDB:                    0,
		EquipmentCatalogPath:       "data/equipment/catalog.json",
		RewardDurationSec:          10,
		StageLimit:                 3,
		FirstStageSeedBase:         1,
		DirectorMinDifficulty:      0.5,
		DirectorMaxDifficulty:      10,
		DirectorTargetClearTimeSec: 45,
		DirectorTargetDPS:          30,
		DirectorTargetDamageTaken:  50,
		LoginTimeoutSec:            5,
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
	if c.parseErr != nil {
		return c.parseErr
	}
	if c.TickHz <= 0 {
		return fmt.Errorf("config: ODYSSEY_TICK_HZ must be positive, got %d", c.TickHz)
	}
	if c.SnapshotHz <= 0 || c.SnapshotHz > c.TickHz {
		return fmt.Errorf("config: ODYSSEY_SNAPSHOT_HZ must be in (0, %d], got %d", c.TickHz, c.SnapshotHz)
	}
	if c.TCPAddr == "" {
		return fmt.Errorf("config: ODYSSEY_TCP_ADDR must not be empty")
	}
	if err := validateLoopbackListenAddr(c.AdminAddr); err != nil {
		return fmt.Errorf("config: ODYSSEY_ADMIN_ADDR: %w", err)
	}
	if strings.TrimSpace(c.EquipmentCatalogPath) == "" {
		return fmt.Errorf("config: ODYSSEY_EQUIPMENT_CATALOG must not be empty")
	}
	if c.RewardDurationSec < 1 || c.RewardDurationSec > 3600 {
		return fmt.Errorf("config: ODYSSEY_REWARD_DURATION_SEC must be in [1, 3600], got %d", c.RewardDurationSec)
	}
	if c.StageLimit < 3 || c.StageLimit > 1000 {
		return fmt.Errorf("config: ODYSSEY_STAGE_LIMIT must be in [3, 1000], got %d", c.StageLimit)
	}
	for key, value := range map[string]float64{
		"ODYSSEY_DIRECTOR_MIN_DIFFICULTY":        c.DirectorMinDifficulty,
		"ODYSSEY_DIRECTOR_MAX_DIFFICULTY":        c.DirectorMaxDifficulty,
		"ODYSSEY_DIRECTOR_TARGET_CLEAR_TIME_SEC": c.DirectorTargetClearTimeSec,
		"ODYSSEY_DIRECTOR_TARGET_DPS":            c.DirectorTargetDPS,
		"ODYSSEY_DIRECTOR_TARGET_DAMAGE_TAKEN":   c.DirectorTargetDamageTaken,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return fmt.Errorf("config: %s must be a finite positive number, got %v", key, value)
		}
	}
	if c.DirectorMinDifficulty > c.DirectorMaxDifficulty {
		return fmt.Errorf("config: director minimum difficulty must not exceed maximum difficulty")
	}
	return nil
}

func validateLoopbackListenAddr(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("must use a loopback host until Admin authentication is implemented")
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
		applyInt(cfg, key, val, &cfg.RedisDB)
	case "ODYSSEY_EQUIPMENT_CATALOG":
		cfg.EquipmentCatalogPath = val
	case "ODYSSEY_REWARD_DURATION_SEC":
		applyInt(cfg, key, val, &cfg.RewardDurationSec)
	case "ODYSSEY_STAGE_LIMIT":
		applyInt(cfg, key, val, &cfg.StageLimit)
	case "ODYSSEY_FIRST_STAGE_SEED_BASE":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			cfg.recordParseError(key, val, err)
		} else {
			cfg.FirstStageSeedBase = n
		}
	case "ODYSSEY_DIRECTOR_MIN_DIFFICULTY":
		applyFloat(cfg, key, val, &cfg.DirectorMinDifficulty)
	case "ODYSSEY_DIRECTOR_MAX_DIFFICULTY":
		applyFloat(cfg, key, val, &cfg.DirectorMaxDifficulty)
	case "ODYSSEY_DIRECTOR_TARGET_CLEAR_TIME_SEC":
		applyFloat(cfg, key, val, &cfg.DirectorTargetClearTimeSec)
	case "ODYSSEY_DIRECTOR_TARGET_DPS":
		applyFloat(cfg, key, val, &cfg.DirectorTargetDPS)
	case "ODYSSEY_DIRECTOR_TARGET_DAMAGE_TAKEN":
		applyFloat(cfg, key, val, &cfg.DirectorTargetDamageTaken)
	case "ODYSSEY_LOGIN_TIMEOUT_SEC":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.LoginTimeoutSec = n
		}
	}
}

func applyInt(cfg *Config, key, val string, target *int) {
	n, err := strconv.Atoi(val)
	if err != nil {
		cfg.recordParseError(key, val, err)
		return
	}
	*target = n
}

func applyFloat(cfg *Config, key, val string, target *float64) {
	n, err := strconv.ParseFloat(val, 64)
	if err != nil {
		cfg.recordParseError(key, val, err)
		return
	}
	*target = n
}

func (c *Config) recordParseError(key, val string, err error) {
	if c.parseErr == nil {
		c.parseErr = fmt.Errorf("config: parse %s=%q: %w", key, val, err)
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
