package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

func TestParityProjectionsCoverFrozenDashboardCollections(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	jobs := projectJobs(navigateTo(model, RouteWork, ScreenWorkJobs))
	telemetry := projectTelemetry(model)
	instances := projectInstances(navigateTo(model, RouteFleet, ScreenFleetInstances))
	org := projectLiveOrg(navigateTo(model, RouteFleet, ScreenFleetOrg))
	topology := projectTopology(navigateTo(model, RouteFleet, ScreenFleetTopology))
	if len(jobs) != 12 || len(instances) != 6 || len(org) != 7 {
		t.Fatalf("primary projections jobs=%d instances=%d org_roles=%d", len(jobs), len(instances), len(org))
	}
	if len(telemetry.Models) != 4 || len(telemetry.Bounces) != 4 {
		t.Fatalf("telemetry models=%+v bounces=%+v", telemetry.Models, telemetry.Bounces)
	}
	if len(topology.Deployments) != 2 || len(topology.Pipelines) != 4 || len(topology.Budgets) != 2 || len(topology.Schedules) != 5 || len(topology.Deadlines) != 3 || len(topology.Teams) != 3 {
		t.Fatalf("topology projection = %+v", topology)
	}
}

func TestBounceTelemetryPreservesCountPrecedenceAndRejectsCounterfeitKickoff(t *testing.T) {
	snapshot := &daemonclient.Snapshot{Resources: map[string]*daemonclient.Resource{}}
	job := &daemonclient.Job{ID: "counted", URI: "agt://dep/job/counted", OutcomeURI: "agt://dep/outcome/counted"}
	snapshot.Resources[job.URI] = testResource(job.URI, "job", job.ID, map[string]any{
		"kickoff": "## Review findings (bounce 1)\nClass: infra\n## Review findings (bounce 2)\nClass: scope",
	})
	snapshot.Resources[job.OutcomeURI] = testResource(job.OutcomeURI, "outcome", job.ID, map[string]any{
		"bounce_classes": map[string]any{"capability": 2, "scope": 1},
		"bounces":        map[string]any{"infra": 9},
	})
	counts := bounceCountsForJob(snapshot, job)
	if len(counts) != 2 || counts["capability"] != 2 || counts["scope"] != 1 {
		t.Fatalf("explicit outcome count precedence = %v", counts)
	}
	delete(snapshot.Resources, job.OutcomeURI)
	counts = bounceCountsForJob(snapshot, job)
	if counts["infra"] != 1 || counts["scope"] != 1 || len(counts) != 2 {
		t.Fatalf("multi-bounce kickoff counts = %v", counts)
	}
	snapshot.Resources[job.URI] = testResource(job.URI, "job", job.ID, map[string]any{"kickoff": "infra failure outside an accepted bounce heading"})
	if counterfeit := bounceCountsForJob(snapshot, job); len(counterfeit) != 0 {
		t.Fatalf("counterfeit non-bounce kickoff classified = %v", counterfeit)
	}
}

func TestParityQueriesAreScreenLocalWithRepeatedValueOR(t *testing.T) {
	model := navigateTo(smallFixtureModel(Capabilities{}), RouteWork, ScreenWorkJobs)
	model, _ = Update(model, QueryChanged{Value: "status:running status:blocked"})
	if model.QueryError != "" || len(projectJobs(model)) != 5 {
		t.Fatalf("job repeated-value query error=%q rows=%d", model.QueryError, len(projectJobs(model)))
	}
	model, _ = Update(model, QueryChanged{Value: "phase:running"})
	if model.QueryError == "" || len(projectJobs(model)) != 12 {
		t.Fatalf("unknown job field error=%q rows=%d", model.QueryError, len(projectJobs(model)))
	}
	model = navigateTo(model, RouteFleet, ScreenFleetInstances)
	model, _ = Update(model, QueryChanged{Value: "agent:worker status:running"})
	if model.QueryError != "" || len(projectInstances(model)) != 2 {
		t.Fatalf("instance query error=%q rows=%d", model.QueryError, len(projectInstances(model)))
	}
}

func TestParitySubroutesUseOneNavigationAndSemanticFocus(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model, _ = Update(model, Key{Name: "g", At: fixtureTime})
	model, _ = Update(model, Key{Name: "w", At: fixtureTime})
	if model.Route != RouteWork || model.Screen != ScreenWorkJobs {
		t.Fatalf("g w = route %s screen %s", model.Route, model.Screen)
	}
	model, _ = Update(model, Key{Name: "]", At: fixtureTime})
	if model.Screen != ScreenWorkTelemetry {
		t.Fatalf("work subsection = %s", model.Screen)
	}
	model = navigateTo(model, RouteFleet, ScreenFleetTopology)
	model.FocusIndex = 2
	model = preserveFocus(model)
	selected := model.Focus.ItemID
	model, _ = Update(model, Resize{Width: 80, Height: 24})
	model, _ = Update(model, Resize{Width: 160, Height: 50})
	if selected == "" || model.Focus.ItemID != selected {
		t.Fatalf("semantic topology focus changed across resize: %q -> %q", selected, model.Focus.ItemID)
	}
	model, _ = Update(model, Key{Name: "]", At: fixtureTime})
	if model.TopologySection != TopologyPipelines || model.Focus.ItemID == selected {
		t.Fatalf("topology section/focus = %s/%q", model.TopologySection, model.Focus.ItemID)
	}
}

func TestEveryParityScreenCanonicalRenderStableAndPlainModes(t *testing.T) {
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"no_color", Capabilities{}}, {"term_dumb", Capabilities{Dumb: true}}}
	for _, screen := range parityScreens {
		for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
			for _, mode := range modes {
				name := fmt.Sprintf("%s/%dx%d/%s", screen, geometry[0], geometry[1], mode.name)
				t.Run(name, func(t *testing.T) {
					model := navigateTo(smallFixtureModel(mode.caps), routeForScreen(screen), screen)
					model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
					first, second := Render(model), Render(model)
					if first != second {
						t.Fatal("consecutive renders differ")
					}
					plain := sgrPattern.ReplaceAllString(first, "")
					assertFrameGeometry(t, plain, geometry[0], geometry[1])
					if mode.name == "color" && !strings.Contains(first, "\x1b[") {
						t.Fatal("color render has no SGR")
					}
					if mode.name != "color" {
						for index, value := range []byte(first) {
							if value == 0x1b || value == 0x9b || value == 0x9d {
								t.Fatalf("plain render control byte %#x at %d", value, index)
							}
						}
					}
					if mode.name == "term_dumb" {
						for index, value := range []byte(first) {
							if value >= utf8.RuneSelf {
								t.Fatalf("TERM=dumb non-ASCII byte %#x at %d", value, index)
							}
						}
					}
				})
			}
		}
	}
}

func TestLargeFleetParityScreensFirstPaint(t *testing.T) {
	for _, screen := range parityScreens {
		model := navigateTo(largeFixtureModel(), routeForScreen(screen), screen)
		model, _ = Update(model, Resize{Width: 160, Height: 50})
		started := time.Now()
		frame := Render(model)
		elapsed := time.Since(started)
		assertFrameGeometry(t, frame, 160, 50)
		if elapsed > 150*time.Millisecond {
			t.Errorf("%s first paint = %s, limit 150ms", screen, elapsed)
		}
	}
}

func TestParityScreenGoldenFiles(t *testing.T) {
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"no_color", Capabilities{}}, {"term_dumb", Capabilities{Dumb: true}}}
	for _, screen := range parityScreens {
		for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
			for _, mode := range modes {
				model := navigateTo(smallFixtureModel(mode.caps), routeForScreen(screen), screen)
				model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
				frame := []byte(Render(model))
				name := strings.ReplaceAll(string(screen), "/", "_")
				path := filepath.Join("testdata", fmt.Sprintf("%s_%dx%d_%s.golden", name, geometry[0], geometry[1], mode.name))
				if os.Getenv("UPDATE_TUI_GOLDENS") == "1" {
					if err := os.WriteFile(path, frame, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(frame, want) {
					t.Errorf("golden mismatch: %s", path)
				}
			}
		}
	}
}

func TestTopologySubsectionGoldenFiles(t *testing.T) {
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"no_color", Capabilities{}}, {"term_dumb", Capabilities{Dumb: true}}}
	for _, section := range topologySections {
		for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
			for _, mode := range modes {
				model := navigateTo(smallFixtureModel(mode.caps), RouteFleet, ScreenFleetTopology)
				model.TopologySection = section
				model.FocusIndex = 2
				model = preserveFocus(model)
				model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
				frame := []byte(Render(model))
				path := filepath.Join("testdata", fmt.Sprintf("fleet_topology_%s_%dx%d_%s.golden", section, geometry[0], geometry[1], mode.name))
				if os.Getenv("UPDATE_TUI_GOLDENS") == "1" {
					if err := os.WriteFile(path, frame, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(frame, want) {
					t.Errorf("golden mismatch: %s", path)
				}
			}
		}
	}
}
