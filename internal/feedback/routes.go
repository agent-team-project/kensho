package feedback

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Route struct {
	Name string
	Type string
	Root string
}

type routeConfigFile struct {
	Feedback feedbackRouteConfig `toml:"feedback"`
}

type feedbackRouteConfig struct {
	Upstream string                    `toml:"upstream"`
	Routes   map[string]rawRouteConfig `toml:"routes"`
}

type rawRouteConfig struct {
	Type string `toml:"type"`
	Kind string `toml:"kind"`
	Root string `toml:"root"`
}

func ResolveRoute(teamDir, name string) (Route, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Route{}, errors.New("route name is required")
	}
	cfg, err := loadRouteConfig(teamDir)
	if err != nil {
		return Route{}, err
	}
	raw, ok := cfg.Feedback.Routes[name]
	if !ok {
		return Route{}, fmt.Errorf("feedback route %q is not declared", name)
	}
	kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(raw.Type, raw.Kind)))
	if kind == "" {
		return Route{}, fmt.Errorf("feedback route %q is missing type/kind", name)
	}
	route := Route{Name: name, Type: kind}
	if kind == "local" {
		root := strings.TrimSpace(raw.Root)
		if root == "" {
			return Route{}, fmt.Errorf("feedback route %q type=local requires root", name)
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(filepath.Dir(teamDir), root)
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return Route{}, fmt.Errorf("feedback route %q root: %w", name, err)
		}
		route.Root = filepath.Clean(abs)
	}
	return route, nil
}

func loadRouteConfig(teamDir string) (*routeConfigFile, error) {
	path := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &routeConfigFile{}, nil
		}
		return nil, err
	}
	var cfg routeConfigFile
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}
