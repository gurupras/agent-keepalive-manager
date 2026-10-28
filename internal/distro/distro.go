// Package distro describes the distribution a binary was built for: where it
// reports metrics, where it looks for updates and where its packages live.
//
// Every value is set at link time from a profile file (profiles/*.env) with
//
//	-ldflags "-X github.com/gurupras/agent-keepalive-manager/internal/distro.metricsURL=..."
//
// so one source tree produces both the public build and any organisation's
// own build without either one's addresses appearing in the other. A build
// made without a profile (plain `go build`) has no metrics endpoint and no
// update feed: it never contacts anything.
package distro

import "strconv"

// Link-time values. Strings only, because -X can set nothing else.
var (
	name            = "dev"
	vendor          = ""
	homepage        = "https://github.com/gurupras/agent-keepalive-manager"
	metricsURL      = ""
	metricsDefault  = "on"
	metricsLocked   = "false"
	updateFeedURL   = ""
	updatePublicKey = ""
	downloadURL     = ""
	repoURL         = ""
)

// Profile is the build's distribution profile.
type Profile struct {
	// Name identifies the profile ("public", an organisation's name, "dev").
	Name     string `json:"name"`
	Vendor   string `json:"vendor,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	// MetricsURL is the hub that receives usage reports; "" disables them.
	MetricsURL string `json:"metricsUrl,omitempty"`
	// MetricsDefault is whether reporting is on before the user chooses.
	MetricsDefault bool `json:"metricsDefault"`
	// MetricsLocked forbids the user from changing the metrics settings
	// (an administrator's config file can still change them).
	MetricsLocked bool `json:"metricsLocked"`
	// UpdateFeedURL is the signed release feed (latest.json); "" disables updates.
	UpdateFeedURL string `json:"updateFeedUrl,omitempty"`
	// UpdatePublicKey is the hex Ed25519 key the feed must be signed with.
	UpdatePublicKey string `json:"-"`
	// DownloadURL is the human download page.
	DownloadURL string `json:"downloadUrl,omitempty"`
	// RepoURL is the base of the apt/rpm/arch repositories.
	RepoURL string `json:"repoUrl,omitempty"`
}

// Current returns the profile this binary was linked with.
func Current() Profile {
	return Profile{
		Name:            name,
		Vendor:          vendor,
		Homepage:        homepage,
		MetricsURL:      metricsURL,
		MetricsDefault:  parseBool(metricsDefault, true),
		MetricsLocked:   parseBool(metricsLocked, false),
		UpdateFeedURL:   updateFeedURL,
		UpdatePublicKey: updatePublicKey,
		DownloadURL:     downloadURL,
		RepoURL:         repoURL,
	}
}

func parseBool(s string, def bool) bool {
	switch s {
	case "on", "yes":
		return true
	case "off", "no":
		return false
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return def
}
