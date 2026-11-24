package setup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

// PlaneCloudURL is Plane cloud's API base URL.
const PlaneCloudURL = "https://api.plane.so"

// PlaneInput is what the Plane step asks for. The API key itself is never
// asked for or stored: only the name of the variable holding it.
type PlaneInput struct {
	Cloud     bool
	BaseURL   string // self-hosted only
	Workspace string
	KeyEnv    string
}

var (
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// PlaneInputFrom fills the form from the current config.
func PlaneInputFrom(p config.Plane) PlaneInput {
	in := PlaneInput{Cloud: true, Workspace: p.Workspace, KeyEnv: p.APIKeyEnv}
	if b := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/"); b != "" && b != PlaneCloudURL {
		in.Cloud, in.BaseURL = false, b
	}
	if in.KeyEnv == "" {
		in.KeyEnv = "PLANE_API_KEY"
	}
	return in
}

// Base is the API base URL the input selects.
func (in PlaneInput) Base() string {
	if in.Cloud {
		return PlaneCloudURL
	}
	return strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
}

// Validate checks the input before anything is tested or written.
func (in PlaneInput) Validate() error {
	if !in.Cloud {
		u, err := url.Parse(in.Base())
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("base URL %q must look like https://plane.example.com", in.BaseURL)
		}
	}
	if !slugRe.MatchString(strings.TrimSpace(in.Workspace)) {
		return fmt.Errorf("workspace slug %q: use the part after the host in Plane URLs (lowercase letters, digits, - and _)", in.Workspace)
	}
	if !envNameRe.MatchString(strings.TrimSpace(in.KeyEnv)) {
		return fmt.Errorf("%q is not an environment variable name", in.KeyEnv)
	}
	return nil
}

// Config is the input as config, for testing the connection.
func (in PlaneInput) Config(base config.Plane) config.Plane {
	base.BaseURL = in.Base()
	base.Workspace = strings.TrimSpace(in.Workspace)
	base.APIKeyEnv = strings.TrimSpace(in.KeyEnv)
	return base
}

// Sets are the config values the step writes.
func (in PlaneInput) Sets() []config.Set {
	return []config.Set{
		{Key: "plane.base_url", Value: in.Base()},
		{Key: "plane.workspace_slug", Value: strings.TrimSpace(in.Workspace)},
		{Key: "plane.api_key_env", Value: strings.TrimSpace(in.KeyEnv)},
	}
}

// ErrNoKey means the API key variable isn't set in this environment.
var ErrNoKey = errors.New("the API key variable is not set")

// PlaneCheck is the outcome of testing a Plane connection.
type PlaneCheck struct {
	User     string
	Projects []plane.Project
}

// TestPlane connects with the input's settings and lists the workspace's
// projects. The key is read from the named variable (or a configured
// api_key, which wins as in forge itself). It returns ErrNoKey when the
// variable is empty, so the step can show the line to add.
func TestPlane(ctx context.Context, getenv func(string) string, newPlane func(config.Plane, string) (plane.API, error),
	in PlaneInput, base config.Plane,
) (PlaneCheck, error) {
	if err := in.Validate(); err != nil {
		return PlaneCheck{}, err
	}
	cfg := in.Config(base)
	key := cfg.APIKey
	if key == "" {
		key = strings.TrimSpace(getenv(cfg.APIKeyEnv))
	}
	if key == "" {
		return PlaneCheck{}, ErrNoKey
	}
	api, err := newPlane(cfg, key)
	if err != nil {
		return PlaneCheck{}, err
	}
	u, err := api.Me(ctx)
	if err != nil {
		if plane.IsAuthError(err) {
			return PlaneCheck{}, fmt.Errorf("the API key was rejected: create one in Plane (Profile settings → API tokens): %w", err)
		}
		return PlaneCheck{}, fmt.Errorf("can't reach Plane at %s: %w", cfg.BaseURL, err)
	}
	name := u.DisplayName
	if name == "" {
		name = u.Email
	}
	ps, err := api.Projects(ctx)
	if err != nil {
		return PlaneCheck{}, fmt.Errorf("connected as %s, but listing the projects of %q failed (check the workspace slug): %w", name, cfg.Workspace, err)
	}
	return PlaneCheck{User: name, Projects: ps}, nil
}

// ShellLine is where and how to export the API key variable for the user's
// shell ($SHELL): the rc file and the exact line to add.
func ShellLine(shell, keyEnv string) (rcFile, line string) {
	switch filepath.Base(shell) {
	case "bash":
		return "~/.bashrc", fmt.Sprintf(`export %s="<your Plane API key>"`, keyEnv)
	case "fish":
		return "~/.config/fish/config.fish", fmt.Sprintf(`set -gx %s "<your Plane API key>"`, keyEnv)
	default:
		return "~/.zshrc", fmt.Sprintf(`export %s="<your Plane API key>"`, keyEnv)
	}
}
