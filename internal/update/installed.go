package update

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
)

// A package manager (or the user) can replace the program while the server
// runs. The server keeps running the old version until it restarts, so
// watch tracks the program file and offers a restart once a newer version
// is in place: State "ready", Action "restart".

// stamp identifies one version of a file.
type stamp struct {
	size int64
	mod  time.Time
}

func stampOf(path string) (stamp, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return stamp{}, false
	}
	return stamp{fi.Size(), fi.ModTime()}, true
}

// watch checks the program file every Options.WatchEvery until ctx ends,
// against the file as it was when New ran.
// A change counts once the file has stayed the same for a whole interval,
// so a file that is still being written is not run.
func (u *Updater) watch(ctx context.Context) {
	target, probed := u.in.Target, u.started
	var seen stamp
	t := time.NewTicker(u.opts.WatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s, ok := stampOf(target)
		if !ok || s == probed {
			continue
		}
		if s != seen {
			seen = s
			continue
		}
		probed = s
		v, err := u.opts.probe(ctx, target)
		if err != nil {
			if ctx.Err() == nil {
				u.opts.Log.Info("update: the program file changed but its version cannot be read", "path", target, "err", err)
			}
			continue
		}
		u.installed(v)
	}
}

// installed records that version v is now in place of the running program.
func (u *Updater) installed(v string) {
	u.mu.Lock()
	switch {
	case u.busy || u.st.State == apitypes.UpdateInstalling:
		// Our own update put it there and restarts by itself.
		u.mu.Unlock()
		return
	case !Newer(v, u.opts.Version):
		if u.st.Installed == "" {
			u.mu.Unlock()
			return
		}
		// Put back the running version (or an older one): nothing to offer
		// until the next feed check.
		u.st.Installed, u.st.Latest, u.st.Action = "", nil, ""
		u.st.State = apitypes.UpdateIdle
	default:
		u.opts.Log.Info("update: a newer version is installed; restart to use it", "version", v)
		u.st.Installed = v
		if u.st.Latest == nil || u.st.Latest.Version != v {
			u.st.Latest = &apitypes.Latest{Version: v}
		}
		u.st.State, u.st.Action, u.st.Command = apitypes.UpdateReady, apitypes.ActionRestart, ""
		u.st.Progress, u.st.Error = 0, ""
	}
	u.mu.Unlock()
	u.notify()
}

// probeVersion runs `path version` and returns the version it prints.
func probeVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	hide(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(out.String(), "\n")
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != "agent-keepalive" {
		return "", fmt.Errorf("unexpected output %q", line)
	}
	if _, ok := ParseVersion(f[1]); !ok {
		return "", fmt.Errorf("not a release version: %q", f[1])
	}
	return f[1], nil
}
