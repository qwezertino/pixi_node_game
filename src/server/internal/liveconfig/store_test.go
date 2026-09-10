package liveconfig

import (
	"net/url"
	"os"
	"testing"

	"pixi_game_server/internal/config"
)

func baselineLiveNetConfig() *config.LiveNetConfig {
	return &config.LiveNetConfig{
		WorldWidth: 6000, WorldHeight: 3000,
		MaxConnections: 12000, MessageRateLimit: 120, BurstLimit: 20,
		IPConnBurst: 20, KeyframeDivisor: 100, FanoutQueueShedDepth: 6,
		FanoutDropStreak: 120, WriteBatchSize: 8,
		FanoutFairDebtMax: 12, FanoutFairDebtInc: 1, FanoutFairDebtDec: 2,
		FanoutMinRecipientsPerTick: 256, FanoutMaxRecipientsPerTick: 1000,
		FanoutTarget:                6_000_000,
		WorldStateActiveStalenessNs: 220_000_000, WorldStateIdleStalenessNs: 650_000_000, WorldStateActiveWindowNs: 1_000_000_000,
		SpawnMinX: 1500, SpawnMaxX: 3000, SpawnMinY: 500, SpawnMaxY: 1500,
	}
}

func TestResolveLiveNetPatchAllowsUnrelatedKeyDespiteInvalidLegacyRow(t *testing.T) {
	raw := map[string]string{
		"max_connections": "999999999",
	}
	patch := map[string]string{"keyframe_divisor": "50"}

	resolved, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch)
	if err != nil {
		t.Fatalf("unrelated valid key change should succeed despite invalid legacy max_connections, got %v", err)
	}
	if resolved.KeyframeDivisor != 50 {
		t.Fatalf("expected keyframe_divisor to be applied, got %d", resolved.KeyframeDivisor)
	}
	if resolved.MaxConnections != baselineLiveNetConfig().MaxConnections {
		t.Fatalf("expected invalid legacy max_connections to fall back to baseline, got %d", resolved.MaxConnections)
	}
}

func TestResolveLiveNetPatchCanFixTheInvalidKeyItself(t *testing.T) {
	raw := map[string]string{
		"max_connections": "999999999",
	}
	patch := map[string]string{"max_connections": "5000"}

	resolved, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch)
	if err != nil {
		t.Fatalf("fixing the invalid key itself should succeed, got %v", err)
	}
	if resolved.MaxConnections != 5000 {
		t.Fatalf("expected max_connections to be fixed to 5000, got %d", resolved.MaxConnections)
	}
}

func TestResolveLiveNetPatchAppliesMultipleKeysAtomically(t *testing.T) {
	raw := map[string]string{}
	patch := map[string]string{
		"fanout_min_recipients_per_tick": "800",
		"fanout_max_recipients_per_tick": "500",
	}

	if _, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch); err == nil {
		t.Fatalf("expected an internally-inconsistent min/max patch to be rejected")
	}

	patch = map[string]string{
		"fanout_min_recipients_per_tick": "100",
		"fanout_max_recipients_per_tick": "200",
	}
	resolved, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch)
	if err != nil {
		t.Fatalf("expected consistent min/max patch to succeed, got %v", err)
	}
	if resolved.FanoutMinRecipientsPerTick != 100 || resolved.FanoutMaxRecipientsPerTick != 200 {
		t.Fatalf("expected both patched keys to apply together, got min=%d max=%d", resolved.FanoutMinRecipientsPerTick, resolved.FanoutMaxRecipientsPerTick)
	}
}

func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		old, had := os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

func TestPostgresDSNFromEnvEscapesSpecialCharacters(t *testing.T) {
	withEnv(t, map[string]string{
		"POSTGRES_HOST":     "db.example.com",
		"POSTGRES_PORT":     "5432",
		"POSTGRES_USER":     "game",
		"POSTGRES_PASSWORD": "p@ss/word?with#special",
		"POSTGRES_DB":       "game",
	})

	dsn := postgresDSNFromEnv()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN failed to parse as a URL: %v (%s)", err, dsn)
	}
	if u.Scheme != "postgres" {
		t.Fatalf("expected postgres scheme, got %q", u.Scheme)
	}
	if u.User.Username() != "game" {
		t.Fatalf("expected user %q, got %q", "game", u.User.Username())
	}
	pw, ok := u.User.Password()
	if !ok || pw != "p@ss/word?with#special" {
		t.Fatalf("password was mangled by DSN construction: got %q ok=%v", pw, ok)
	}
	if u.Hostname() != "db.example.com" || u.Port() != "5432" {
		t.Fatalf("host/port mismatch: %q %q", u.Hostname(), u.Port())
	}
	if u.Path != "/game" {
		t.Fatalf("expected db path /game, got %q", u.Path)
	}
	if u.Query().Get("sslmode") != "disable" {
		t.Fatalf("expected sslmode=disable, got %q", u.Query().Get("sslmode"))
	}
}
