// Package config is agent-keepalive's runtime configuration: the few
// settings that a distribution profile sets, an administrator may override
// or freeze, and a user may change.
//
// Layers, strongest first (architecture.md, Distribution profiles):
//
//  1. the admin file (per-OS path, see AdminPath), whose "locked" list
//     freezes keys;
//  2. the environment (AGENT_KEEPALIVE_METRICS, AGENT_KEEPALIVE_METRICS_URL,
//     AGENT_KEEPALIVE_METRICS_TOKEN);
//  3. the user file, <data dir>/config.json;
//  4. the distribution profile (internal/distro);
//  5. built-in defaults.
//
// A locked key ignores the user and environment layers, because both are
// under the user's control: it takes the admin value if the admin file
// sets one, else the profile's. A profile with METRICS_LOCKED locks
// metrics.enabled and metrics.url the same way.
//
// Both files use nested JSON objects, for example
//
//	{"metrics": {"enabled": true, "url": "https://hub.example"}, "locked": ["metrics.enabled"]}
//
// and keys are named by their dotted path.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
)

// Keys.
const (
	MetricsEnabled = "metrics.enabled" // bool
	MetricsURL     = "metrics.url"     // string: the hub; "" disables reporting
	MetricsToken   = "metrics.token"   // string: bearer token for the hub; never shown
)

// Layer names a configuration layer (Setting.Source, Setting.LockedBy).
type Layer string

const (
	LayerDefault Layer = "default"
	LayerProfile Layer = "profile"
	LayerUser    Layer = "user"
	LayerEnv     Layer = "env"
	LayerAdmin   Layer = "admin"
)

type kind int

const (
	kindBool kind = iota
	kindString
)

type keyDef struct {
	kind kind
	env  string
	// secret values are reported as set/unset only.
	secret bool
}

var keys = map[string]keyDef{
	MetricsEnabled: {kind: kindBool, env: "AGENT_KEEPALIVE_METRICS"},
	MetricsURL:     {kind: kindString, env: "AGENT_KEEPALIVE_METRICS_URL"},
	MetricsToken:   {kind: kindString, env: "AGENT_KEEPALIVE_METRICS_TOKEN", secret: true},
}

// Keys returns every known key, sorted.
func Keys() []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Setting is the effective value of one key and where it came from.
type Setting struct {
	Key string `json:"key"`
	// Value is a bool or a string. A secret key's value is never included;
	// Set tells whether it has one.
	Value  any   `json:"value,omitempty"`
	Secret bool  `json:"secret,omitempty"`
	IsSet  bool  `json:"set"`
	Source Layer `json:"source"` // the layer the value came from
	Locked bool  `json:"locked"`
	// LockedBy is LayerAdmin or LayerProfile when Locked.
	LockedBy Layer `json:"lockedBy,omitempty"`
}

// Options says where the layers come from. Zero fields take the defaults.
type Options struct {
	// DataDir holds the user file (config.json). Required.
	DataDir string
	// AdminPath overrides AdminPath(); "-" means no admin file.
	AdminPath string
	// Profile defaults to distro.Current().
	Profile *distro.Profile
	// Getenv defaults to os.Getenv.
	Getenv func(string) string
}

// Config is the loaded configuration. It is safe for concurrent use.
type Config struct {
	opts     Options
	userPath string

	mu       sync.Mutex
	admin    map[string]any // flattened, typed
	locked   map[string]bool
	user     map[string]any // flattened, typed
	userRaw  map[string]any // the user file as read, so unknown keys survive a write
	adminErr error
	userErr  error
	onChange []func()
}

// AdminPath is the per-OS location of the administrator's config file.
func AdminPath() string {
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "agent-keepalive", "config.json")
	case "darwin":
		return "/Library/Application Support/agent-keepalive/config.json"
	default:
		return "/etc/agent-keepalive/config.json"
	}
}

// Load reads both files. A missing file is not an error. A malformed file
// is ignored (its layer is empty) and reported by Problems, so a typo in a
// config file never stops the server.
func Load(opts Options) (*Config, error) {
	if opts.DataDir == "" {
		return nil, errors.New("config: no data directory")
	}
	if opts.AdminPath == "" {
		opts.AdminPath = AdminPath()
	}
	if opts.Profile == nil {
		p := distro.Current()
		opts.Profile = &p
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	c := &Config{opts: opts, userPath: filepath.Join(opts.DataDir, "config.json")}
	c.Reload()
	return c, nil
}

// Reload re-reads both files.
func (c *Config) Reload() {
	c.mu.Lock()
	c.admin, c.locked, c.adminErr = nil, map[string]bool{}, nil
	if c.opts.AdminPath != "-" {
		raw, err := readJSON(c.opts.AdminPath)
		if err != nil {
			c.adminErr = err
		} else if raw != nil {
			c.admin, c.adminErr = flatten(raw)
			if l, ok := raw["locked"].([]any); ok {
				for _, k := range l {
					if s, ok := k.(string); ok {
						c.locked[s] = true
					}
				}
			}
		}
	}
	c.user, c.userRaw, c.userErr = nil, nil, nil
	raw, err := readJSON(c.userPath)
	if err != nil {
		c.userErr = err
	} else {
		c.userRaw = raw
		if raw != nil {
			c.user, c.userErr = flatten(raw)
		}
	}
	fns := slices.Clone(c.onChange)
	c.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// Problems describes config files that could not be read, for the log.
func (c *Config) Problems() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []error
	if c.adminErr != nil {
		out = append(out, fmt.Errorf("admin config %s: %w", c.opts.AdminPath, c.adminErr))
	}
	if c.userErr != nil {
		out = append(out, fmt.Errorf("user config %s: %w", c.userPath, c.userErr))
	}
	return out
}

// OnChange registers fn to run after Set or Reload changed anything.
func (c *Config) OnChange(fn func()) {
	c.mu.Lock()
	c.onChange = append(c.onChange, fn)
	c.mu.Unlock()
}

// Get returns the effective value of key with its provenance. An unknown
// key returns a zero Setting with Source "".
func (c *Config) Get(key string) Setting {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.get(key)
}

func (c *Config) get(key string) Setting {
	s, _ := c.resolve(key)
	return s
}

// resolve computes the effective value of key. The Setting hides a secret
// value; the second result never does.
func (c *Config) resolve(key string) (Setting, any) {
	def, ok := keys[key]
	if !ok {
		return Setting{Key: key}, nil
	}
	s := Setting{Key: key, Secret: def.secret}
	s.Locked, s.LockedBy = c.lockedBy(key)

	val, src := c.profileValue(key, def)
	if !s.Locked {
		if v, ok := c.user[key]; ok {
			val, src = v, LayerUser
		}
		if e := c.opts.Getenv(def.env); def.env != "" && e != "" {
			if v, err := parse(def.kind, e); err == nil {
				val, src = v, LayerEnv
			}
		}
	}
	if v, ok := c.admin[key]; ok {
		val, src = v, LayerAdmin
	}
	s.Source = src
	if str, ok := val.(string); ok {
		s.IsSet = str != ""
	} else {
		s.IsSet = true
	}
	if !def.secret {
		s.Value = val
	}
	return s, val
}

func (c *Config) lockedBy(key string) (bool, Layer) {
	if c.locked[key] {
		return true, LayerAdmin
	}
	if c.opts.Profile.MetricsLocked && (key == MetricsEnabled || key == MetricsURL) {
		return true, LayerProfile
	}
	return false, ""
}

func (c *Config) profileValue(key string, def keyDef) (any, Layer) {
	p := c.opts.Profile
	switch key {
	case MetricsEnabled:
		return p.MetricsDefault, LayerProfile
	case MetricsURL:
		if p.MetricsURL != "" {
			return p.MetricsURL, LayerProfile
		}
	}
	if def.kind == kindBool {
		return false, LayerDefault
	}
	return "", LayerDefault
}

// Bool returns the effective value of a boolean key.
func (c *Config) Bool(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.rawValue(key).(bool); ok {
		return b
	}
	return false
}

// String returns the effective value of a string key (secrets included).
func (c *Config) String(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.rawValue(key).(string); ok {
		return s
	}
	return ""
}

func (c *Config) rawValue(key string) any {
	_, v := c.resolve(key)
	return v
}

// Effective returns every key's setting, sorted by key.
func (c *Config) Effective() []Setting {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Setting, 0, len(keys))
	for _, k := range Keys() {
		out = append(out, c.get(k))
	}
	return out
}

// Set stores a user value for key in <data dir>/config.json. It returns
// apitypes.ErrLocked (wrapped) when the key is locked. Setting a value that
// the admin file or the environment overrides is allowed; the stored value
// applies once the override is gone. A nil value removes the user value.
func (c *Config) Set(key string, value any) error {
	def, ok := keys[key]
	if !ok {
		return fmt.Errorf("config: unknown key %q", key)
	}
	if value != nil {
		switch def.kind {
		case kindBool:
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("config: %s must be true or false", key)
			}
		case kindString:
			if _, ok := value.(string); !ok {
				return fmt.Errorf("config: %s must be a string", key)
			}
		}
	}
	c.mu.Lock()
	if locked, by := c.lockedBy(key); locked {
		c.mu.Unlock()
		return fmt.Errorf("%s is set by the %s configuration: %w", key, by, apitypes.ErrLocked)
	}
	// Re-read the file so a concurrent edit by hand is not lost.
	raw, err := readJSON(c.userPath)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("config: %s is not valid JSON; fix or remove it first: %w", c.userPath, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	setPath(raw, strings.Split(key, "."), value)
	if err := writeJSON(c.userPath, raw); err != nil {
		c.mu.Unlock()
		return err
	}
	c.userRaw = raw
	c.user, c.userErr = flatten(raw)
	fns := slices.Clone(c.onChange)
	c.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	return nil
}

func readJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeJSON replaces path atomically, so a crash never leaves half a file.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// flatten extracts the known keys from a nested object, checking types. A
// key of the wrong type is an error but does not hide the others.
func flatten(raw map[string]any) (map[string]any, error) {
	out := map[string]any{}
	var errs []error
	for key, def := range keys {
		v, ok := lookupPath(raw, strings.Split(key, "."))
		if !ok || v == nil {
			continue
		}
		switch def.kind {
		case kindBool:
			switch t := v.(type) {
			case bool:
				out[key] = t
			case string:
				if b, err := parse(kindBool, t); err == nil {
					out[key] = b
					continue
				}
				errs = append(errs, fmt.Errorf("%s: want true or false", key))
			default:
				errs = append(errs, fmt.Errorf("%s: want true or false", key))
			}
		case kindString:
			if s, ok := v.(string); ok {
				out[key] = s
			} else {
				errs = append(errs, fmt.Errorf("%s: want a string", key))
			}
		}
	}
	return out, errors.Join(errs...)
}

func lookupPath(m map[string]any, path []string) (any, bool) {
	v, ok := m[path[0]]
	if !ok {
		return nil, false
	}
	if len(path) == 1 {
		return v, true
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return lookupPath(sub, path[1:])
}

func setPath(m map[string]any, path []string, v any) {
	if len(path) == 1 {
		if v == nil {
			delete(m, path[0])
		} else {
			m[path[0]] = v
		}
		return
	}
	sub, ok := m[path[0]].(map[string]any)
	if !ok {
		sub = map[string]any{}
		m[path[0]] = sub
	}
	setPath(sub, path[1:], v)
	if len(sub) == 0 {
		delete(m, path[0])
	}
}

func parse(k kind, s string) (any, error) {
	if k == kindString {
		return s, nil
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "yes", "1", "true":
		return true, nil
	case "off", "no", "0", "false":
		return false, nil
	}
	return strconv.ParseBool(s)
}
