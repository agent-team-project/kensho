// Package agentteam exposes the bundled starter team (the "default" template)
// to the Go binary via `go:embed`.
//
// Why this lives at the module root: `go:embed` patterns cannot escape the
// directory of the source file containing the directive (no `..`, no
// symlinks). The canonical template tree on disk is
// `cli/src/agent_team/template/`, shared with the Python CLI's
// `importlib.resources` lookup. To embed it from Go we put the directive at
// the module root where it can name the path directly. The template
// relocation flagged in SQU-21 is deferred to SQU-23.
package agentteam

import "embed"

//go:embed all:cli/src/agent_team/template
var templateFS embed.FS

// TemplateFS returns the embedded template filesystem.
//
// The bundled tree appears inside the FS under
// `cli/src/agent_team/template/`. Use `TemplateRoot` as the prefix to walk
// from.
func TemplateFS() embed.FS {
	return templateFS
}

// TemplateRoot is the path inside `TemplateFS()` at which the bundled
// starter template begins.
const TemplateRoot = "cli/src/agent_team/template"
