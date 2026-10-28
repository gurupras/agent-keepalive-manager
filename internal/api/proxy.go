package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
)

// Upstream locates the server that runs the scheduler: its port on
// 127.0.0.1 and its API token. It is called per request, so the holder may
// change (it restarted, or this server took the lock).
type Upstream func() (port int, token string, err error)

// errNoUpstream answers 502 when no holder can be found.
var errNoUpstream = errors.New("the agent-keepalive server that runs the scheduler is not answering")

type ctxKey struct{}

type target struct {
	port  int
	token string
}

// newProxy forwards API requests (WebSocket upgrades included) to the
// upstream. The guard already ran on this server; the request is then
// rewritten to look like it came from the upstream's own UI: its Host, its
// Origin, its token. Fetch Metadata headers are dropped because they
// described the browser's view of this server, which was checked.
func newProxy(up Upstream, log *slog.Logger) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(ctxKey{}).(target)
			host := net.JoinHostPort("127.0.0.1", strconv.Itoa(t.port))
			pr.SetURL(&url.URL{Scheme: "http", Host: host})
			pr.Out.Host = host
			h := pr.Out.Header
			h.Set(TokenHeader, t.token)
			if h.Get("Origin") != "" {
				h.Set("Origin", "http://"+host)
			}
			for _, k := range []string{"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User", "Cookie", "Referer"} {
				h.Del(k)
			}
			if q := pr.Out.URL.Query(); q.Has("token") {
				q.Set("token", t.token)
				pr.Out.URL.RawQuery = q.Encode()
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("proxy to scheduler server", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusBadGateway, sentence(errNoUpstream.Error()))
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port, token, err := up()
		if err != nil || port == 0 {
			if err == nil {
				err = errNoUpstream
			}
			log.Warn("proxy to scheduler server", "err", err)
			writeError(w, http.StatusBadGateway, sentence(errNoUpstream.Error()))
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, target{port: port, token: token})
		rp.ServeHTTP(w, r.WithContext(ctx))
	})
}
