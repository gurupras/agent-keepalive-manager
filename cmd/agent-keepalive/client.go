package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
)

// client calls a running server's API with its token.
type client struct {
	base  string // "http://127.0.0.1:4344/"
	token string
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// do sends a request with a JSON body (nil: none) and decodes a JSON answer
// into out (nil: discard). An error answer becomes an error with its
// message.
func (c *client) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Keepalive-Token", c.token)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return &apiError{status: resp.StatusCode, msg: e.Error}
		}
		return &apiError{status: resp.StatusCode, msg: resp.Status}
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// Is lets callers test API errors against the shared sentinels.
func (e *apiError) Is(target error) bool {
	switch target {
	case apitypes.ErrLocked:
		return e.status == http.StatusForbidden
	case apitypes.ErrUnavailable:
		return e.status == http.StatusConflict
	}
	return false
}

// remoteMetrics is apitypes.Metrics over a running server's API.
type remoteMetrics struct{ c *client }

func (m remoteMetrics) Status() apitypes.MetricsStatus {
	var st apitypes.MetricsStatus
	_ = m.c.do("GET", "api/metrics", nil, &st)
	return st
}

func (m remoteMetrics) SetEnabled(on bool) error {
	return m.c.do("PUT", "api/metrics", map[string]bool{"enabled": on}, nil)
}

func (m remoteMetrics) Preview() (any, error) {
	var v json.RawMessage
	err := m.c.do("GET", "api/metrics/preview", nil, &v)
	return v, err
}

func (m remoteMetrics) ResetID() error { return m.c.do("POST", "api/metrics/reset-id", nil, nil) }

// remoteUpdater is apitypes.Updater over a running server's API. It has no
// events; callers poll Status.
type remoteUpdater struct {
	c       *client
	lastErr error
}

func (u *remoteUpdater) Status() apitypes.UpdateStatus {
	var st apitypes.UpdateStatus
	u.lastErr = u.c.do("GET", "api/update", nil, &st)
	return st
}

func (u *remoteUpdater) Check(ctx context.Context) error {
	var st apitypes.UpdateStatus
	if err := u.c.do("POST", "api/update/check", nil, &st); err != nil {
		return err
	}
	if st.State == apitypes.UpdateError {
		return errors.New(st.Error)
	}
	return nil
}

func (u *remoteUpdater) Apply(ctx context.Context) error {
	return u.c.do("POST", "api/update/apply", nil, nil)
}

func (u *remoteUpdater) Subscribe(func(apitypes.UpdateStatus)) func() { return func() {} }

var (
	_ apitypes.Metrics = remoteMetrics{}
	_ apitypes.Updater = (*remoteUpdater)(nil)
)

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func printf(w io.Writer, format string, a ...any) { fmt.Fprintf(w, format, a...) }
