package migrations

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestApplyRejectsNilDatabase(t *testing.T) {
	if err := Apply(context.Background(), nil); !errors.Is(err, ErrNilDatabase) {
		t.Fatalf("Apply(nil) error = %v, want %v", err, ErrNilDatabase)
	}
}

func TestEmbeddedMigrationContainsIdempotencyAndPlayerDetail(t *testing.T) {
	payload, err := files.ReadFile("001_match_results.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, required := range []string{"PRIMARY KEY (match_id)", "payload_sha256 BINARY(32)", "CREATE TABLE IF NOT EXISTS match_players", "FOREIGN KEY (match_id)"} {
		if !strings.Contains(text, required) {
			t.Errorf("migration missing %q", required)
		}
	}
}
