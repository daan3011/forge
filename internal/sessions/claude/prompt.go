package claude

import (
	"regexp"
	"strings"
)

// Claude Code's permission prompt, as captured from its pane (v2.1.x):
//
//	Do you want to proceed?            (or "Do you want to create a.txt?", ...)
//	❯ 1. Yes
//	  2. Yes, and always allow ...
//	  3. No
//	Esc to cancel · Tab to amend
//
// These patterns are the only place forge knows what it looks like; if a
// Claude Code release changes the text, update them and the fixtures in
// testdata/.
var (
	promptQuestionRe = regexp.MustCompile(`^\W*Do you want to .*\?\s*$`)
	promptYesRe      = regexp.MustCompile(`^\W*1\.\s+Yes\b`)
)

// promptWindow is how many lines after the question the "1. Yes" choice
// may appear.
const promptWindow = 4

// PermissionPromptVisible reports whether a captured pane shows Claude
// Code's permission prompt: a "Do you want to …?" question followed by a
// numbered "1. Yes" choice.
func PermissionPromptVisible(pane string) bool {
	lines := strings.Split(pane, "\n")
	for i, l := range lines {
		if !promptQuestionRe.MatchString(l) {
			continue
		}
		for _, next := range lines[i+1 : min(i+1+promptWindow, len(lines))] {
			if promptYesRe.MatchString(next) {
				return true
			}
		}
	}
	return false
}
