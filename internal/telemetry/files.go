package telemetry

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Files in the data dir.
const (
	installIDFile = "install-id"
	queueDir      = "metrics-queue"
	stateFile     = "metrics-state.json"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// newUUID returns a random (version 4) UUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// readInstallID returns the stored install id, or "" if there is none (or
// the file does not hold one).
func readInstallID(dataDir string) string {
	b, err := os.ReadFile(filepath.Join(dataDir, installIDFile))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(b))
	if !uuidRe.MatchString(id) {
		return ""
	}
	return id
}

// writeInstallID makes and stores a new install id.
func writeInstallID(dataDir string) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dataDir, installIDFile), []byte(id+"\n")); err != nil {
		return "", err
	}
	return id, nil
}

// writeAtomic replaces path with data, so a reader sees the old or the new
// content, never a part.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// state is what the reporter remembers between runs.
type state struct {
	// Active is whether reporting was on (with a hub) when last seen.
	Active bool `json:"active"`
	// Through is the last day whose report was built (or skipped).
	Through string `json:"through,omitempty"`
	// Seen are the recent days the server ran on with reporting on; a day
	// without activity is reported only if it is here.
	Seen      []string  `json:"seen,omitempty"`
	LastSent  time.Time `json:"lastSent,omitzero"`
	LastError string    `json:"lastError,omitempty"`
	// Announced is the version whose first report went out early, the
	// day's report so far (Reporter.announce).
	Announced string `json:"announced,omitempty"`
}

func loadState(dataDir string) (state, bool) {
	var s state
	b, err := os.ReadFile(filepath.Join(dataDir, stateFile))
	if err != nil {
		return s, false
	}
	if json.Unmarshal(b, &s) != nil {
		return state{}, false
	}
	return s, true
}

func saveState(dataDir string, s state) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dataDir, stateFile), b)
}

// queue is the directory of reports waiting to be sent, one file per day.
type queue string

func (q queue) path(day string) string { return filepath.Join(string(q), day+".json") }

func (q queue) put(day string, body []byte) error { return writeAtomic(q.path(day), body) }

func (q queue) remove(day string) error {
	err := os.Remove(q.path(day))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (q queue) read(day string) ([]byte, error) { return os.ReadFile(q.path(day)) }

// days lists the queued days, oldest first.
func (q queue) days() []string {
	entries, err := os.ReadDir(string(q))
	if err != nil {
		return nil
	}
	var days []string
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".json")
		if _, valid := parseDay(day); ok && valid && e.Type().IsRegular() {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	return days
}

// drop deletes the queue.
func (q queue) drop() error { return os.RemoveAll(string(q)) }
