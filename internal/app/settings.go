package app

import "github.com/gurupras/agent-keepalive-manager/internal/config"

// MetricsSettings adapts the runtime config to what usage reporting needs
// (telemetry.Settings): it reads the effective metrics keys and stores the
// user's choice, which the config refuses when it is locked.
type MetricsSettings struct{ Config *config.Config }

// Enabled reports whether reporting is on, whether that is fixed, and by
// whom ("admin" or "profile").
func (m MetricsSettings) Enabled() (on, locked bool, by string) {
	s := m.Config.Get(config.MetricsEnabled)
	return m.Config.Bool(config.MetricsEnabled), s.Locked, string(s.LockedBy)
}

// URL is the hub; "" when this build reports nowhere.
func (m MetricsSettings) URL() string { return m.Config.String(config.MetricsURL) }

// Token is the hub's bearer token, if any.
func (m MetricsSettings) Token() string { return m.Config.String(config.MetricsToken) }

// SetEnabled persists the user's choice.
func (m MetricsSettings) SetEnabled(on bool) error { return m.Config.Set(config.MetricsEnabled, on) }

// Experiments reports whether the install takes part in experiments.
func (m MetricsSettings) Experiments() (on, locked bool, by string) {
	s := m.Config.Get(config.ExperimentsEnabled)
	return m.Config.Bool(config.ExperimentsEnabled), s.Locked, string(s.LockedBy)
}

// SetExperiments persists the user's choice.
func (m MetricsSettings) SetExperiments(on bool) error {
	return m.Config.Set(config.ExperimentsEnabled, on)
}

// Enrolled reports whether experiments run: the install takes part and its
// usage reports, which carry the results, go to a hub.
func (m MetricsSettings) Enrolled() bool {
	on, _, _ := m.Enabled()
	exp, _, _ := m.Experiments()
	return on && exp && m.URL() != ""
}
