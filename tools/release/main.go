// Command release writes and checks the signed update feed of a release
// (docs/release-artifacts.md).
//
//	go run ./tools/release keygen
//	    prints a new Ed25519 key pair: the public key (hex, the profile's
//	    UPDATE_PUBLIC_KEY) and the seed (hex, the secret UPDATE_SIGNING_KEY).
//
//	go run ./tools/release feed -version V -base-url URL [-notes notes.md] [-page URL] -out latest.json FILE...
//	    writes latest.json for the dist files FILE..., each downloaded from
//	    URL/<name>, signed with $UPDATE_SIGNING_KEY. Files that are not
//	    release artifacts (hub archives, SHA256SUMS, ...) are skipped. Without
//	    a signing key it refuses, unless -allow-unsigned (local testing only:
//	    the updater never accepts an unsigned feed).
//
//	go run ./tools/release verify [-key HEX] latest.json
//	    checks the feed's signature (key default: $UPDATE_PUBLIC_KEY).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: release keygen | feed ... | verify ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Stdout)
	case "feed":
		err = feedCmd(os.Args[2:])
	case "verify":
		err = verifyCmd(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func keygen(w io.Writer) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "UPDATE_PUBLIC_KEY=%s\nUPDATE_SIGNING_KEY=%s\n", hex.EncodeToString(pub), hex.EncodeToString(priv.Seed()))
	return nil
}

type feedArgs struct {
	version, baseURL, notes, page, out string
	allowUnsigned                      bool
	signingKey                         string
	files                              []string
	now                                time.Time
}

func feedCmd(args []string) error {
	fs := flag.NewFlagSet("feed", flag.ContinueOnError)
	var a feedArgs
	fs.StringVar(&a.version, "version", "", "release version, e.g. 1.5.0")
	fs.StringVar(&a.baseURL, "base-url", "", "URL of the version folder the files are downloaded from")
	fs.StringVar(&a.notes, "notes", "", "markdown file with the release notes")
	fs.StringVar(&a.page, "page", "", "URL of the release page")
	fs.StringVar(&a.out, "out", "latest.json", "feed file to write")
	fs.BoolVar(&a.allowUnsigned, "allow-unsigned", false, "write an unsigned feed when UPDATE_SIGNING_KEY is not set (local testing only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a.files = fs.Args()
	a.signingKey = os.Getenv("UPDATE_SIGNING_KEY")
	a.now = time.Now()
	f, err := buildFeed(a, os.Stderr)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.out + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, a.out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "release: %s: version %s, %d artifacts\n", a.out, f.Version, len(f.Artifacts))
	return nil
}

func buildFeed(a feedArgs, log io.Writer) (*update.Feed, error) {
	v := strings.TrimPrefix(a.version, "v")
	if _, ok := update.ParseVersion(v); !ok {
		return nil, fmt.Errorf("-version %q is not a semantic version", a.version)
	}
	if a.baseURL == "" || len(a.files) == 0 {
		return nil, errors.New("-base-url and at least one file are required")
	}
	var key ed25519.PrivateKey
	switch {
	case strings.TrimSpace(a.signingKey) != "":
		k, err := update.ParseSigningKey(a.signingKey)
		if err != nil {
			return nil, fmt.Errorf("UPDATE_SIGNING_KEY: %w", err)
		}
		key = k
	case !a.allowUnsigned:
		return nil, errors.New("UPDATE_SIGNING_KEY is not set; refusing to write an unsigned feed (use -allow-unsigned for local testing)")
	}
	f := &update.Feed{
		Version:     v,
		PublishedAt: a.now.UTC().Truncate(time.Second).Format(time.RFC3339),
		Page:        a.page,
		Artifacts:   []update.Artifact{},
	}
	if a.notes != "" {
		b, err := os.ReadFile(a.notes)
		if err != nil {
			return nil, err
		}
		f.Notes = strings.TrimSpace(string(b))
	}
	base := strings.TrimRight(a.baseURL, "/")
	seen := map[target]string{}
	for _, p := range a.files {
		name := filepath.Base(p)
		t, ok := classify(name, v)
		if !ok {
			fmt.Fprintf(log, "release: skipping %s\n", name)
			continue
		}
		if prev, dup := seen[t]; dup {
			return nil, fmt.Errorf("%s and %s are both %s %s/%s", prev, name, t.kind, t.os, t.arch)
		}
		seen[t] = name
		size, sum, err := hashFile(p)
		if err != nil {
			return nil, err
		}
		f.Artifacts = append(f.Artifacts, update.Artifact{
			Kind: t.kind, OS: t.os, Arch: t.arch, Name: name,
			URL: base + "/" + name, SHA256: sum, Size: size,
		})
	}
	sort.Slice(f.Artifacts, func(i, j int) bool { return f.Artifacts[i].Name < f.Artifacts[j].Name })
	if len(f.Artifacts) == 0 {
		return nil, errors.New("none of the files is a release artifact of version " + v)
	}
	if key == nil {
		fmt.Fprintln(log, "release: WARNING: the feed is unsigned; no updater will accept it")
		return f, nil
	}
	if err := f.Sign(key); err != nil {
		return nil, err
	}
	pub := hex.EncodeToString(key.Public().(ed25519.PublicKey))
	if err := f.Verify(pub); err != nil {
		return nil, fmt.Errorf("self-check: %w", err)
	}
	fmt.Fprintf(log, "release: signed with public key %s\n", pub)
	return f, nil
}

func verifyCmd(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	key := fs.String("key", os.Getenv("UPDATE_PUBLIC_KEY"), "hex Ed25519 public key (default $UPDATE_PUBLIC_KEY)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: release verify [-key HEX] latest.json")
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	f, err := update.ParseFeed(b, *key)
	if err != nil {
		return err
	}
	fmt.Printf("ok: version %s, published %s, %d artifacts\n", f.Version, f.PublishedAt, len(f.Artifacts))
	for _, a := range f.Artifacts {
		fmt.Printf("  %-16s %-7s %-5s %s\n", a.Kind, a.OS, a.Arch, a.Name)
	}
	return nil
}

func hashFile(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}
