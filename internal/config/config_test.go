package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
)

type env map[string]string

func (e env) get(k string) string { return e[k] }

func load(t *testing.T, admin, user string, p distro.Profile, e env) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	adminPath := filepath.Join(dir, "admin.json")
	if admin != "" {
		if err := os.WriteFile(adminPath, []byte(admin), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(dir, "data")
	if user != "" {
		os.MkdirAll(data, 0o700)
		if err := os.WriteFile(filepath.Join(data, "config.json"), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Load(Options{DataDir: data, AdminPath: adminPath, Profile: &p, Getenv: e.get})
	if err != nil {
		t.Fatal(err)
	}
	return c, data
}

var public = distro.Profile{Name: "public", MetricsURL: "https://hub.example", MetricsDefault: true}

func TestPrecedence(t *testing.T) {
	c, _ := load(t, "", "", public, env{})
	if s := c.Get(MetricsEnabled); s.Value != true || s.Source != LayerProfile {
		t.Fatalf("profile: %+v", s)
	}
	if s := c.Get(MetricsToken); s.IsSet || s.Source != LayerDefault {
		t.Fatalf("default token: %+v", s)
	}

	c, _ = load(t, "", `{"metrics":{"enabled":false}}`, public, env{})
	if s := c.Get(MetricsEnabled); s.Value != false || s.Source != LayerUser {
		t.Fatalf("user: %+v", s)
	}

	c, _ = load(t, "", `{"metrics":{"enabled":false}}`, public, env{"AGENT_KEEPALIVE_METRICS": "on"})
	if s := c.Get(MetricsEnabled); s.Value != true || s.Source != LayerEnv {
		t.Fatalf("env: %+v", s)
	}

	c, _ = load(t, `{"metrics":{"enabled":false,"url":"https://corp"}}`, "", public,
		env{"AGENT_KEEPALIVE_METRICS": "on", "AGENT_KEEPALIVE_METRICS_URL": "https://env"})
	if s := c.Get(MetricsEnabled); s.Value != false || s.Source != LayerAdmin || s.Locked {
		t.Fatalf("admin: %+v", s)
	}
	if got := c.String(MetricsURL); got != "https://corp" {
		t.Fatalf("admin url: %q", got)
	}
}

func TestSecretHidden(t *testing.T) {
	c, _ := load(t, "", "", public, env{"AGENT_KEEPALIVE_METRICS_TOKEN": "s3cret"})
	s := c.Get(MetricsToken)
	if s.Value != nil || !s.IsSet || !s.Secret || s.Source != LayerEnv {
		t.Fatalf("secret: %+v", s)
	}
	if c.String(MetricsToken) != "s3cret" {
		t.Fatal("String must return the secret")
	}
}

func TestAdminLock(t *testing.T) {
	c, _ := load(t, `{"locked":["metrics.enabled"]}`, `{"metrics":{"enabled":false}}`, public,
		env{"AGENT_KEEPALIVE_METRICS": "off"})
	s := c.Get(MetricsEnabled)
	// Locked without an admin value: the user and env layers are ignored.
	if !s.Locked || s.LockedBy != LayerAdmin || s.Value != true || s.Source != LayerProfile {
		t.Fatalf("locked: %+v", s)
	}
	if err := c.Set(MetricsEnabled, false); !errors.Is(err, apitypes.ErrLocked) {
		t.Fatalf("Set on locked key: %v", err)
	}
	if err := c.Set(MetricsURL, "https://mine"); err != nil {
		t.Fatalf("unlocked key: %v", err)
	}
}

func TestProfileLock(t *testing.T) {
	p := public
	p.MetricsLocked = true
	c, _ := load(t, `{"metrics":{"enabled":false}}`, "", p, env{"AGENT_KEEPALIVE_METRICS": "on"})
	s := c.Get(MetricsEnabled)
	if !s.Locked || s.LockedBy != LayerProfile || s.Value != false || s.Source != LayerAdmin {
		t.Fatalf("profile lock, admin value: %+v", s)
	}
	if err := c.Set(MetricsEnabled, true); !errors.Is(err, apitypes.ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if s := c.Get(MetricsToken); s.Locked {
		t.Fatalf("token must not be locked by the profile: %+v", s)
	}
}

func TestSetPersistsAndKeepsUnknownKeys(t *testing.T) {
	c, data := load(t, "", `{"other":{"x":1},"metrics":{"url":"https://a"}}`, public, env{})
	changed := 0
	c.OnChange(func() { changed++ })
	if err := c.Set(MetricsEnabled, false); err != nil {
		t.Fatal(err)
	}
	if changed != 1 || c.Bool(MetricsEnabled) {
		t.Fatalf("changed=%d enabled=%v", changed, c.Bool(MetricsEnabled))
	}
	c2, err := Load(Options{DataDir: data, AdminPath: "-", Profile: &public, Getenv: env{}.get})
	if err != nil {
		t.Fatal(err)
	}
	if c2.Bool(MetricsEnabled) || c2.String(MetricsURL) != "https://a" {
		t.Fatalf("reloaded: %+v", c2.Effective())
	}
	raw, _ := readJSON(filepath.Join(data, "config.json"))
	if _, ok := raw["other"]; !ok {
		t.Fatalf("unknown key dropped: %v", raw)
	}
	if err := c.Set(MetricsEnabled, nil); err != nil || !c.Bool(MetricsEnabled) {
		t.Fatalf("remove: %v %v", err, c.Get(MetricsEnabled))
	}
	if err := c.Set(MetricsEnabled, "yes"); err == nil {
		t.Fatal("wrong type accepted")
	}
	if err := c.Set("nope", true); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestMalformedFilesIgnored(t *testing.T) {
	c, _ := load(t, `{not json`, `{"metrics":{"enabled":"maybe"}}`, public, env{})
	if len(c.Problems()) != 2 {
		t.Fatalf("problems: %v", c.Problems())
	}
	if !c.Bool(MetricsEnabled) {
		t.Fatal("bad values must fall back to the profile")
	}
}

func TestNoProfileHub(t *testing.T) {
	c, _ := load(t, "", "", distro.Profile{Name: "dev", MetricsDefault: true}, env{})
	if s := c.Get(MetricsURL); s.IsSet || s.Source != LayerDefault {
		t.Fatalf("%+v", s)
	}
}
