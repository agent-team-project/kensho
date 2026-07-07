package daemon

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

//go:embed ui
var daemonUI embed.FS

func daemonUIHandler(build buildinfo.Info) http.Handler {
	static, err := fs.Sub(daemonUI, "ui")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErrorWithBuild(w, http.StatusInternalServerError, "daemon ui unavailable", build)
		})
	}
	files := http.StripPrefix("/ui/", http.FileServer(http.FS(static)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			writeErrorWithBuild(w, http.StatusMethodNotAllowed, "method not allowed", build)
			return
		}
		if r.URL.Path == "/ui" {
			http.Redirect(w, r, "/ui/", http.StatusPermanentRedirect)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}
