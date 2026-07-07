package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/origin"
)

const (
	daemonTransportTCP = "tcp"
	authRejectedAction = "auth_rejected"
)

type daemonTransportContextKey struct{}
type bearerOriginContextKey struct{}

func daemonConnContext(ctx context.Context, c net.Conn) context.Context {
	if c != nil && strings.HasPrefix(c.LocalAddr().Network(), "tcp") {
		return context.WithValue(ctx, daemonTransportContextKey{}, daemonTransportTCP)
	}
	return ctx
}

func loopbackAuthHandler(next http.Handler, teamDir string, mgr *InstanceManager, build buildinfo.Info) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	daemonRoot := ""
	if mgr != nil {
		daemonRoot = mgr.daemonRoot
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackAuthRequired(r) {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearerTokenFromRequest(r)
		if !ok {
			recordAuthRejected(daemonRoot, r, "missing bearer token")
			writeAuthError(w, build, "daemon http auth: missing bearer token")
			return
		}
		identity, ok := lookupBearerToken(teamDir, daemonRoot, token)
		if !ok {
			recordAuthRejected(daemonRoot, r, "invalid bearer token")
			writeAuthError(w, build, "daemon http auth: invalid bearer token")
			return
		}
		if !identity.Operator {
			r = requestWithBearerOrigin(r, identity.Origin)
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackAuthRequired(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Context().Value(daemonTransportContextKey{}) != daemonTransportTCP {
		return false
	}
	// The static UI shell (HTML/CSS/JS, no data) is served unauthenticated so a
	// browser can load the page and reach its token field; the /v1 data endpoints
	// the page then calls still require the bearer token.
	if isStaticUIPath(r.URL.Path) {
		return false
	}
	return true
}

// isStaticUIPath reports whether p addresses the embedded UI shell (/ui, /ui/…),
// which carries no data and is safe to serve without a bearer token.
func isStaticUIPath(p string) bool {
	return p == "/ui" || p == "/ui/" || strings.HasPrefix(p, "/ui/")
}

func bearerTokenFromRequest(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return "", false
	}
	scheme, token, ok := strings.Cut(raw, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func requestWithBearerOrigin(r *http.Request, tokenOrigin origin.Envelope) *http.Request {
	if r == nil {
		return r
	}
	tokenOrigin = tokenOrigin.Clean()
	if tokenOrigin.Empty() {
		return r
	}
	fromHeader, _ := origin.ParseHeaderValue(r.Header.Get(origin.HeaderName))
	merged := origin.Merge(tokenOrigin, fromHeader)
	ctx := context.WithValue(r.Context(), bearerOriginContextKey{}, tokenOrigin)
	clone := r.Clone(ctx)
	clone.Header = r.Header.Clone()
	if rendered := origin.HeaderValue(merged); rendered != "" {
		clone.Header.Set(origin.HeaderName, rendered)
	}
	return clone
}

func trustedBearerOriginFromRequest(r *http.Request) (origin.Envelope, bool) {
	if r == nil {
		return origin.Envelope{}, false
	}
	value, ok := r.Context().Value(bearerOriginContextKey{}).(origin.Envelope)
	if !ok {
		return origin.Envelope{}, false
	}
	value = value.Clean()
	if value.Empty() {
		return origin.Envelope{}, false
	}
	return value, true
}

func writeAuthError(w http.ResponseWriter, build buildinfo.Info, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="agent-teamd"`)
	writeErrorWithBuild(w, http.StatusUnauthorized, message, build)
}

func recordAuthRejected(daemonRoot string, r *http.Request, reason string) {
	if strings.TrimSpace(daemonRoot) == "" {
		return
	}
	remote := ""
	path := ""
	if r != nil {
		remote = r.RemoteAddr
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	msg := fmt.Sprintf("daemon http auth rejected: %s", reason)
	if path != "" || remote != "" {
		msg = fmt.Sprintf("%s path=%s remote=%s", msg, path, remote)
	}
	_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
		Action:  authRejectedAction,
		TS:      time.Now().UTC(),
		Message: msg,
	})
}
