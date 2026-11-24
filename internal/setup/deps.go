package setup

import (
	"strings"

	"github.com/daan3011/forge/internal/doctor"
)

// DepChecks are the doctor checks the dependencies step runs.
var DepChecks = []string{
	doctor.NameGit, doctor.NameGH, doctor.NameTmux, doctor.NameClaude,
	doctor.NameDelta, doctor.NameChafa, doctor.NameRsvg,
}

// GitHubChecks are the doctor checks the GitHub step runs (gh includes
// the login).
var GitHubChecks = []string{doctor.NameGH}

// DepsResult sums up the dependency checks: failed when a required tool is
// missing or too old, done otherwise (optional tools only warn).
func DepsResult(cs []doctor.Check) Result {
	var missing, fixes []string
	warns := 0
	for _, c := range cs {
		switch c.Status {
		case doctor.Fail:
			missing = append(missing, c.Name)
			if c.Fix != "" {
				fixes = append(fixes, c.Fix)
			}
		case doctor.Warn:
			warns++
		}
	}
	if len(missing) > 0 {
		return Result{Status: Failed, Detail: "missing or too old: " + strings.Join(missing, ", "), Fix: strings.Join(fixes, "; ")}
	}
	r := Result{Status: Done, Detail: "all required tools found"}
	if warns > 0 {
		r.Detail += " (some optional ones missing)"
	}
	return r
}

// GitHubResult sums up the gh check.
func GitHubResult(cs []doctor.Check) Result {
	for _, c := range cs {
		if c.Name != doctor.NameGH {
			continue
		}
		if c.Status == doctor.Fail {
			return Result{Status: Failed, Detail: c.Detail, Fix: c.Fix}
		}
		return Result{Status: Done, Detail: c.Detail}
	}
	return Result{Status: Failed, Detail: "gh wasn't checked", Fix: "gh auth status"}
}

// NeedsLogin reports whether the gh check says gh is installed but not
// logged in, so offering `gh auth login` makes sense.
func NeedsLogin(cs []doctor.Check) bool {
	for _, c := range cs {
		if c.Name == doctor.NameGH && c.Status == doctor.Fail && c.Fix == "gh auth login" {
			return true
		}
	}
	return false
}
