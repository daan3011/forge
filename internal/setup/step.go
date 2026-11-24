// Package setup is the logic of forge's guided first-run setup (P6.6):
// each step's checks, inputs and results, without any UI. The wizard in
// internal/setup/wizard drives it; everything external goes through Env so
// tests run on fakes.
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StepID names a setup step; it is also what `forge setup <step>` takes.
type StepID string

// The steps, in order.
const (
	StepDeps       StepID = "deps"
	StepGitHub     StepID = "github"
	StepPlane      StepID = "plane"
	StepProjects   StepID = "projects"
	StepTmux       StepID = "tmux"
	StepAnimations StepID = "animations"
	StepCapture    StepID = "capture"
	StepSmoke      StepID = "smoke"
)

// Step describes one step.
type Step struct {
	ID    StepID
	Title string
	// Blurb is the one-line explanation under the title.
	Blurb string
}

// Steps are every step in the order the wizard walks them.
var Steps = []Step{
	{StepDeps, "Dependencies", "git, gh, tmux and claude, plus the optional delta, chafa and rsvg-convert"},
	{StepGitHub, "GitHub", "forge reads PRs and CI through your gh login"},
	{StepPlane, "Plane", "work items from Plane cloud or your own instance (optional)"},
	{StepProjects, "Projects", "where forge looks for your repos, and their Plane projects"},
	{StepTmux, "tmux", "forge's own tmux server for agent sessions, and the key back to forge"},
	{StepAnimations, "Animations", "embers, sparks and the hammer: full, reduced or off"},
	{StepCapture, "Quick capture", "Alt+c in your own tmux opens forge capture (optional)"},
	{StepSmoke, "Smoke test", "a throwaway Claude Code session proves hooks reach forge"},
}

// StepIDs are the ids of Steps, for help text and validation.
func StepIDs() []string {
	out := make([]string, len(Steps))
	for i, s := range Steps {
		out[i] = string(s.ID)
	}
	return out
}

// ParseStep checks a `forge setup <step>` argument.
func ParseStep(s string) (StepID, error) {
	for _, st := range Steps {
		if string(st.ID) == strings.ToLower(strings.TrimSpace(s)) {
			return st.ID, nil
		}
	}
	return "", fmt.Errorf("unknown setup step %q (one of: %s)", s, strings.Join(StepIDs(), ", "))
}

// StepByID returns the step with id.
func StepByID(id StepID) (Step, bool) {
	for _, s := range Steps {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}

// Status is a step's outcome.
type Status int

// The statuses.
const (
	Pending Status = iota
	Done
	Skipped
	Failed
)

func (s Status) String() string {
	return [...]string{"pending", "done", "skipped", "failed"}[s]
}

// Result is what a step ended with.
type Result struct {
	Status Status `json:"status"`
	// Detail is one line on what happened.
	Detail string `json:"detail,omitempty"`
	// Fix is the command (or action) that fixes a failed or skipped step.
	Fix string `json:"fix,omitempty"`
	// Lines are extra report lines (the smoke test's registration checks).
	Lines []string `json:"lines,omitempty"`
}

// Results are the results by step.
type Results map[StepID]Result

// Store is where setup keeps its state: the setup_completed flag and the
// last result of each step. *store.Settings satisfies it.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	MarkSetupCompleted(ctx context.Context) error
}

// SaveResult remembers r as step id's last result.
func SaveResult(ctx context.Context, st Store, id StepID, r Result) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return st.Set(ctx, stepKey(id), string(b))
}

// LoadResults reads every step's last result; steps never run are absent.
func LoadResults(ctx context.Context, st Store) Results {
	out := Results{}
	for _, s := range Steps {
		v, err := st.Get(ctx, stepKey(s.ID))
		if err != nil {
			continue
		}
		var r Result
		if json.Unmarshal([]byte(v), &r) == nil {
			out[s.ID] = r
		}
	}
	return out
}

// stepKey matches store.SettingSetupStepPrefix.
func stepKey(id StepID) string { return "setup_step:" + string(id) }

// SummaryLine is one line of the summary screen.
type SummaryLine struct {
	Step   Step
	Result Result
	// Fix is what to run: the step's own fix, else `forge setup <step>`.
	Fix string
}

// Summary lists the steps that were skipped or failed (or never run), each
// with the command that fixes it, in step order.
func Summary(rs Results, only []StepID) []SummaryLine {
	var out []SummaryLine
	for _, s := range Steps {
		if len(only) > 0 && !containsID(only, s.ID) {
			continue
		}
		r := rs[s.ID]
		if r.Status == Done {
			continue
		}
		fix := "forge setup " + string(s.ID)
		if r.Fix != "" {
			fix = r.Fix + "  (then: forge setup " + string(s.ID) + ")"
		}
		out = append(out, SummaryLine{Step: s, Result: r, Fix: fix})
	}
	return out
}

func containsID(ids []StepID, id StepID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
