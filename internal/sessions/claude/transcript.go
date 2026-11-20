package claude

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// DefaultProjectsDir is where Claude Code keeps session transcripts:
// ~/.claude/projects/<encoded cwd>/<session id>.jsonl. It is "" when the
// home directory is unknown.
func DefaultProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// FindTranscript returns the transcript of session id under projectsDir
// (any project directory), and whether there is one. Claude Code only
// writes a transcript once the conversation has a message, so a session
// that never got one has nothing to --resume.
func FindTranscript(projectsDir, id string) (string, bool) {
	if projectsDir == "" || !ValidSessionID(id) {
		return "", false
	}
	// id is a validated UUID, so it holds no glob metacharacters.
	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", id+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	slices.Sort(matches)
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.Mode().IsRegular() {
			return m, true
		}
	}
	return "", false
}

// uuidInUseRe matches the error claude prints and exits with when
// --session-id names a session that already exists (v2.1.x):
//
//	Error: Session ID <uuid> is already in use.
var uuidInUseRe = regexp.MustCompile(`(?m)Session ID [0-9a-fA-F-]+ is already in use`)

// UUIDInUse reports whether a captured pane shows claude refusing
// --session-id because the id is taken.
func UUIDInUse(pane string) bool { return uuidInUseRe.MatchString(pane) }
