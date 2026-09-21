package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsMatchExample(t *testing.T) {
	c := Default()
	if c.TickHz != 30 || c.SnapshotHz != 10 {
		t.Fatalf("defaults tick/snapshot = %d/%d, want 30/10", c.TickHz, c.SnapshotHz)
	}
	if c.TCPAddr != "127.0.0.1:7777" {
		t.Fatalf("default TCPAddr = %q", c.TCPAddr)
	}
	if c.EquipmentCatalogPath != "data/equipment/catalog.json" || c.RewardDurationSec != 10 || c.StageLimit != 12 {
		t.Fatalf("default gameplay config = %q/%d/%d", c.EquipmentCatalogPath, c.RewardDurationSec, c.StageLimit)
	}
	if c.ResumeEnabled || c.ResumeTTLSeconds != 30 || c.RedisOperationMS != 2000 {
		t.Fatalf("default Resume config = %v/%d/%d", c.ResumeEnabled, c.ResumeTTLSeconds, c.RedisOperationMS)
	}
}

func TestLoadDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "ODYSSEY_TCP_ADDR=127.0.0.1:9000\nODYSSEY_TICK_HZ=60\n# comment\nODYSSEY_LOG_LEVEL=info\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TCPAddr != "127.0.0.1:9000" {
		t.Errorf("TCPAddr = %q, want 127.0.0.1:9000", cfg.TCPAddr)
	}
	if cfg.TickHz != 60 {
		t.Errorf("TickHz = %d, want 60", cfg.TickHz)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	// Untouched keys keep defaults.
	if cfg.SnapshotHz != 10 {
		t.Errorf("SnapshotHz = %d, want default 10", cfg.SnapshotHz)
	}
}

func TestEnvVarsOverrideFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("ODYSSEY_TCP_ADDR=127.0.0.1:9000\n"), 0o600)

	t.Setenv("ODYSSEY_TCP_ADDR", "127.0.0.1:9999")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TCPAddr != "127.0.0.1:9999" {
		t.Errorf("TCPAddr = %q, want env override 127.0.0.1:9999", cfg.TCPAddr)
	}
}

func TestValidateRejectsBadSnapshotHz(t *testing.T) {
	c := Default()
	c.SnapshotHz = 999 // > TickHz
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for SnapshotHz > TickHz")
	}
}

func TestValidateKeepsUnauthenticatedAdminOnLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8080", ":8080", "192.168.1.5:8080", "missing-port"} {
		cfg := Default()
		cfg.AdminAddr = address
		if err := cfg.Validate(); err == nil {
			t.Errorf("AdminAddr %q unexpectedly accepted", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		cfg := Default()
		cfg.AdminAddr = address
		if err := cfg.Validate(); err != nil {
			t.Errorf("AdminAddr %q rejected: %v", address, err)
		}
	}
}

func TestMissingDotEnvIsFine(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TCPAddr != "127.0.0.1:7777" {
		t.Errorf("TCPAddr = %q, want default", cfg.TCPAddr)
	}
}

func TestLoadGameplayConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "ODYSSEY_EQUIPMENT_CATALOG=config/equipment.json\n" +
		"ODYSSEY_REWARD_DURATION_SEC=15\n" +
		"ODYSSEY_STAGE_LIMIT=5\n" +
		"ODYSSEY_FIRST_STAGE_SEED_BASE=-42\n" +
		"ODYSSEY_DIRECTOR_MIN_DIFFICULTY=0.75\n" +
		"ODYSSEY_DIRECTOR_MAX_DIFFICULTY=8.5\n" +
		"ODYSSEY_DIRECTOR_TARGET_CLEAR_TIME_SEC=40\n" +
		"ODYSSEY_DIRECTOR_TARGET_DPS=35.5\n" +
		"ODYSSEY_DIRECTOR_TARGET_DAMAGE_TAKEN=45\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EquipmentCatalogPath != "config/equipment.json" || cfg.RewardDurationSec != 15 || cfg.StageLimit != 5 || cfg.FirstStageSeedBase != -42 {
		t.Fatalf("loaded gameplay config = %+v", cfg)
	}
	if cfg.DirectorMinDifficulty != 0.75 || cfg.DirectorMaxDifficulty != 8.5 || cfg.DirectorTargetDPS != 35.5 {
		t.Fatalf("loaded director config = %+v", cfg)
	}
}

func TestLoadRejectsMalformedGameplayNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("ODYSSEY_STAGE_LIMIT=three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected malformed stage limit to fail")
	}
}

func TestLoadResumeConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "ODYSSEY_RESUME_ENABLED=true\nODYSSEY_RESUME_TTL_SEC=45\nODYSSEY_REDIS_OPERATION_TIMEOUT_MS=750\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ResumeEnabled || cfg.ResumeTTLSeconds != 45 || cfg.RedisOperationMS != 750 {
		t.Fatalf("loaded Resume config = %+v", cfg)
	}

	if err := os.WriteFile(path, []byte("ODYSSEY_RESUME_ENABLED=maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("malformed Resume boolean unexpectedly accepted")
	}
}

func TestValidateResumeProductionPolicy(t *testing.T) {
	cfg := Default()
	cfg.Env = "production"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production accepted disabled Resume store")
	}
	cfg.ResumeEnabled = true
	cfg.ResultsEnabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production Resume config rejected: %v", err)
	}
	for _, mutate := range []func(*Config){
		func(config *Config) { config.RedisAddr = "missing-port" },
		func(config *Config) { config.RedisDB = -1 },
		func(config *Config) { config.ResumeTTLSeconds = 0 },
		func(config *Config) { config.RedisOperationMS = 1 },
	} {
		candidate := *cfg
		mutate(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Errorf("unsafe Resume config accepted: %+v", candidate)
		}
	}
}

func TestLoadAndValidateResultPersistenceConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "ODYSSEY_RESULTS_ENABLED=true\nODYSSEY_RESULT_QUEUE_CAPACITY=64\n" +
		"ODYSSEY_RESULT_MAX_ATTEMPTS=5\nODYSSEY_RESULT_ATTEMPT_TIMEOUT_MS=800\n" +
		"ODYSSEY_RESULT_RETRY_BACKOFF_MS=25\nODYSSEY_RESULT_SHUTDOWN_TIMEOUT_SEC=9\n" +
		"ODYSSEY_RESULT_DEAD_LETTER_PATH=var/test-results.jsonl\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ResultsEnabled || cfg.ResultQueueCapacity != 64 || cfg.ResultMaxAttempts != 5 ||
		cfg.ResultAttemptTimeoutMS != 800 || cfg.ResultRetryBackoffMS != 25 || cfg.ResultShutdownTimeoutSec != 9 ||
		cfg.ResultDeadLetterPath != "var/test-results.jsonl" {
		t.Fatalf("loaded result persistence config = %+v", cfg)
	}
	cfg.ResultQueueCapacity = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid result queue capacity accepted")
	}
}

func TestValidateRejectsUnsafeGameplayConfig(t *testing.T) {
	tests := []func(*Config){
		func(c *Config) { c.EquipmentCatalogPath = "" },
		func(c *Config) { c.RewardDurationSec = 0 },
		func(c *Config) { c.StageLimit = 2 },
		func(c *Config) { c.DirectorMinDifficulty = c.DirectorMaxDifficulty + 1 },
	}
	for index, mutate := range tests {
		cfg := Default()
		mutate(cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("case %d: expected validation failure", index)
		}
	}
}
