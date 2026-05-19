package config_test

import (
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/config"
)

func TestDefaultsAreSafeForP2PMVP(t *testing.T) {
	cfg := config.Defaults()

	if cfg.DefaultP2PTTL != 30*time.Minute {
		t.Fatalf("DefaultP2PTTL = %s, want 30m", cfg.DefaultP2PTTL)
	}
	if cfg.DefaultMaxDownloads != 1 {
		t.Fatalf("DefaultMaxDownloads = %d, want 1", cfg.DefaultMaxDownloads)
	}
	if !cfg.RequireE2E {
		t.Fatal("RequireE2E = false, want true")
	}
	if cfg.Transport != "webrtc_p2p" {
		t.Fatalf("Transport = %q, want webrtc_p2p", cfg.Transport)
	}
}
