package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/bootstrap"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/config"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
)

func TestLoadGameplay(t *testing.T) {
	cfg := config.Default()
	cfg.EquipmentCatalogPath = filepath.Join("..", "..", "..", "data", "equipment", "catalog.json")
	cfg.RewardDurationSec = 12
	cfg.StageLimit = 4
	cfg.FirstStageSeedBase = 99

	got, err := bootstrap.LoadGameplay(cfg, game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Valid() || got.Catalog().Version() != bootstrap.EquipmentCatalogVersion {
		t.Fatalf("invalid gameplay/catalog version: valid=%v version=%d", got.Valid(), got.Catalog().Version())
	}
	if got.RewardDurationTicks() != 12*game.TickRate || got.StageLimit() != 4 {
		t.Fatalf("duration/stage limit = %d/%d", got.RewardDurationTicks(), got.StageLimit())
	}
	if got.FirstStageSeed(7) != 106 {
		t.Fatalf("room seed = %d, want 106", got.FirstStageSeed(7))
	}
}

func TestLoadGameplayRejectsMissingCorruptAndWrongVersionCatalogs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "missing", want: "open equipment catalog"},
		{name: "corrupt", content: "{", want: "parse equipment catalog"},
		{name: "wrong-version", content: `{"version":2,"items":[{"id":1,"key":"p","name":"Potion","description":"heal","slot":"potion","heal":1}]}`, want: "version 2, want 1"},
		{name: "missing-description", content: `{"version":1,"items":[{"id":1,"key":"p","name":"Potion","slot":"potion","heal":1}]}`, want: "description is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.EquipmentCatalogPath = filepath.Join(t.TempDir(), "catalog.json")
			if test.content != "" {
				if err := os.WriteFile(cfg.EquipmentCatalogPath, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := bootstrap.LoadGameplay(cfg, game.DefaultConfig())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadGameplayRejectsInvalidDirectorConfig(t *testing.T) {
	cfg := config.Default()
	cfg.EquipmentCatalogPath = filepath.Join("..", "..", "..", "data", "equipment", "catalog.json")
	cfg.DirectorMinDifficulty = 20
	cfg.DirectorMaxDifficulty = 10
	if _, err := bootstrap.LoadGameplay(cfg, game.DefaultConfig()); err == nil {
		t.Fatal("expected invalid director configuration to fail")
	}
}
