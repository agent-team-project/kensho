// Package agentteam exposes the bundled starter team (the "default" template)
// to the Go binary via `go:embed`.
//
// `go:embed` patterns cannot escape the directory of the source file
// containing the directive (no `..`, no symlinks), so this directive lives at
// the module root and the bundled template tree lives at `template/` next to
// it.
package agentteam

import "embed"

//go:embed all:template
var templateFS embed.FS

// TemplateFS returns the embedded template filesystem. Use `TemplateRoot` as
// the prefix to walk from.
func TemplateFS() embed.FS {
	return templateFS
}

// TemplateRoot is the path inside `TemplateFS()` at which the bundled
// starter template begins.
const TemplateRoot = "template"
