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

func TestMissingDotEnvIsFine(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TCPAddr != "127.0.0.1:7777" {
		t.Errorf("TCPAddr = %q, want default", cfg.TCPAddr)
	}
}
