// Package update keeps agent-keepalive up to date from a signed release feed
// (docs/release-artifacts.md): it detects how this copy was installed,
// checks the feed, and either installs the new release itself (AppImage,
// archive, Windows portable and setup, macOS pkg) or tells the user the
// package-manager command that does (deb, rpm, arch, Homebrew).
//
// This file is shared with tools/release, which writes and signs the feed.
package update

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Feed is latest.json. The field order is the canonical order the
// signature is computed over.
type Feed struct {
	Version     string     `json:"version"`
	PublishedAt string     `json:"publishedAt"` // RFC 3339, UTC
	Notes       string     `json:"notes"`       // markdown
	Page        string     `json:"page"`
	Artifacts   []Artifact `json:"artifacts"`
	Signature   string     `json:"signature"` // hex Ed25519 over Canonical()
}

// Artifact is one downloadable file of a release.
type Artifact struct {
	Kind   string `json:"kind"` // an install kind (Kind* constants)
	OS     string `json:"os"`   // GOOS
	Arch   string `json:"arch"` // GOARCH
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"` // lowercase hex
	Size   int64  `json:"size"`
}

// unsignedFeed is Feed without its signature: the signed form.
type unsignedFeed struct {
	Version     string     `json:"version"`
	PublishedAt string     `json:"publishedAt"`
	Notes       string     `json:"notes"`
	Page        string     `json:"page"`
	Artifacts   []Artifact `json:"artifacts"`
}

// Errors of feed verification.
var (
	ErrNoPublicKey  = errors.New("feed: no public key to verify the feed with")
	ErrBadSignature = errors.New("feed: the signature does not verify")
)

// Canonical returns the bytes the signature covers: the feed without
// Signature, marshalled by encoding/json in field order.
func (f *Feed) Canonical() ([]byte, error) {
	arts := f.Artifacts
	if arts == nil {
		arts = []Artifact{}
	}
	return json.Marshal(unsignedFeed{
		Version:     f.Version,
		PublishedAt: f.PublishedAt,
		Notes:       f.Notes,
		Page:        f.Page,
		Artifacts:   arts,
	})
}

// Sign sets f.Signature with key.
func (f *Feed) Sign(key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return errors.New("feed: malformed signing key")
	}
	b, err := f.Canonical()
	if err != nil {
		return err
	}
	f.Signature = hex.EncodeToString(ed25519.Sign(key, b))
	return nil
}

// Verify checks f.Signature against the hex public key pub. An empty key
// is an error: an unsigned or unverifiable feed is never accepted.
func (f *Feed) Verify(pub string) error {
	key, err := ParsePublicKey(pub)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(strings.TrimSpace(f.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	b, err := f.Canonical()
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, b, sig) {
		return ErrBadSignature
	}
	return nil
}

// ParseFeed decodes latest.json, verifies it against the hex public key
// pub and checks that its version is a semantic version.
func ParseFeed(data []byte, pub string) (*Feed, error) {
	var f Feed
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("feed: %w", err)
	}
	if err := f.Verify(pub); err != nil {
		return nil, err
	}
	if _, ok := ParseVersion(f.Version); !ok {
		return nil, fmt.Errorf("feed: bad version %q", f.Version)
	}
	return &f, nil
}

// Find returns the artifact for kind, goos and goarch.
func (f *Feed) Find(kind, goos, goarch string) (Artifact, bool) {
	for _, a := range f.Artifacts {
		if a.Kind == kind && a.OS == goos && a.Arch == goarch {
			return a, true
		}
	}
	return Artifact{}, false
}

// ParsePublicKey decodes a hex Ed25519 public key ("" is ErrNoPublicKey).
func ParsePublicKey(pub string) (ed25519.PublicKey, error) {
	pub = strings.TrimSpace(pub)
	if pub == "" {
		return nil, ErrNoPublicKey
	}
	b, err := hex.DecodeString(pub)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("feed: malformed public key")
	}
	return ed25519.PublicKey(b), nil
}

// ParseSigningKey decodes a hex Ed25519 seed (UPDATE_SIGNING_KEY).
func ParseSigningKey(seed string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, fmt.Errorf("signing key: want a %d-byte hex Ed25519 seed", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(b), nil
}
