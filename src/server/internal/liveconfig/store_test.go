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

func TestResolveLiveNetPatchRejectsUnrelatedKeyWhenLegacyRowStillInvalid(t *testing.T) {
	raw := map[string]string{
		"max_connections": "999999999",
	}
	patch := map[string]string{"keyframe_divisor": "50"}

	if _, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch); err == nil {
		t.Fatalf("expected an untouched invalid legacy row to fail validation instead of being silently dropped")
	}
}

func TestResolveLiveNetPatchAppliesPatchOnTopOfStoredRelatedKey(t *testing.T) {
	raw := map[string]string{
		"fanout_min_recipients_per_tick": "256",
		"fanout_max_recipients_per_tick": "1000",
	}
	patch := map[string]string{"fanout_min_recipients_per_tick": "800"}

	resolved, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch)
	if err != nil {
		t.Fatalf("patch valid against the actually-stored max should succeed, got %v", err)
	}
	if resolved.FanoutMinRecipientsPerTick != 800 || resolved.FanoutMaxRecipientsPerTick != 1000 {
		t.Fatalf("expected min=800 max=1000 (the stored max, not baseline), got min=%d max=%d",
			resolved.FanoutMinRecipientsPerTick, resolved.FanoutMaxRecipientsPerTick)
	}
}

func TestResolveLiveNetPatchValidatesAgainstStoredValueNotBaseline(t *testing.T) {
	raw := map[string]string{
		"fanout_min_recipients_per_tick": "100",
	}
	patch := map[string]string{"fanout_max_recipients_per_tick": "200"}

	resolved, err := resolveLiveNetPatch(baselineLiveNetConfig(), raw, patch)
	if err != nil {
		t.Fatalf("patch valid against the actually-stored min should not be rejected against baseline's default min, got %v", err)
	}
	if resolved.FanoutMinRecipientsPerTick != 100 || resolved.FanoutMaxRecipientsPerTick != 200 {
		t.Fatalf("expected min=100 (stored) max=200 (patched), got min=%d max=%d",
			resolved.FanoutMinRecipientsPerTick, resolved.FanoutMaxRecipientsPerTick)
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

func TestPerKeyApplyIsOrderDependentUnlikeSingleSnapshotApply(t *testing.T) {
	base := baselineLiveNetConfig()
	base.FanoutMinRecipientsPerTick = 100
	base.FanoutMaxRecipientsPerTick = 200

	minFirst := *base
	live := config.NewLiveNet(&minFirst)
	if err := live.Update(func(c *config.LiveNetConfig) error {
		return c.ApplyKey("fanout_min_recipients_per_tick", "800")
	}); err == nil {
		t.Fatalf("applying min=800 alone against the still-old max=200 should be rejected by the old per-key path")
	}
	if err := live.Update(func(c *config.LiveNetConfig) error {
		return c.ApplyKey("fanout_max_recipients_per_tick", "1000")
	}); err != nil {
		t.Fatalf("applying max=1000 alone should succeed: %v", err)
	}
	if got := live.Load(); got.FanoutMinRecipientsPerTick != 100 || got.FanoutMaxRecipientsPerTick != 1000 {
		t.Fatalf("per-key apply got stuck on a stale min: min=%d max=%d", got.FanoutMinRecipientsPerTick, got.FanoutMaxRecipientsPerTick)
	}

	snapshot := map[string]string{
		"fanout_min_recipients_per_tick": "800",
		"fanout_max_recipients_per_tick": "1000",
	}
	for _, first := range []string{"fanout_min_recipients_per_tick", "fanout_max_recipients_per_tick"} {
		second := "fanout_max_recipients_per_tick"
		if first == second {
			second = "fanout_min_recipients_per_tick"
		}
		copyOfBase := *base
		one := config.NewLiveNet(&copyOfBase)
		if err := one.Update(func(c *config.LiveNetConfig) error {
			if err := c.ApplyKey(first, snapshot[first]); err != nil {
				return err
			}
			return c.ApplyKey(second, snapshot[second])
		}); err != nil {
			t.Fatalf("single-snapshot apply (order %s,%s) should succeed, got %v", first, second, err)
		}
		got := one.Load()
		if got.FanoutMinRecipientsPerTick != 800 || got.FanoutMaxRecipientsPerTick != 1000 {
			t.Fatalf("single-snapshot apply (order %s,%s) gave inconsistent result: min=%d max=%d",
				first, second, got.FanoutMinRecipientsPerTick, got.FanoutMaxRecipientsPerTick)
		}
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
