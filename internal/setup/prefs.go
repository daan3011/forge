package setup

import (
	"context"
	"errors"
	"fmt"

	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/tmux"
)

// AnimationLevels are the choices of the animations step, in order.
var AnimationLevels = []config.Animations{config.AnimationsFull, config.AnimationsReduced, config.AnimationsOff}

// AnimationSets is the config value for level.
func AnimationSets(level config.Animations) ([]config.Set, error) {
	for _, l := range AnimationLevels {
		if l == level {
			return []config.Set{{Key: "ui.animations", Value: string(level)}}, nil
		}
	}
	return nil, fmt.Errorf("animations must be full, reduced or off, not %q", level)
}

// CaptureSnippet is the line for your own ~/.tmux.conf that opens forge
// capture on Alt+c. forge never edits that file.
const CaptureSnippet = tmux.CaptureSnippet

// ErrNoClipboardTool means no copy tool was found; the wizard falls back
// to OSC 52 (tea.SetClipboard), which most terminals support.
var ErrNoClipboardTool = clipboard.ErrNoTool

// CopyCapture copies the snippet. ErrNoClipboardTool asks for the OSC 52
// fallback.
func CopyCapture(ctx context.Context, c clipboard.Copier) error {
	if c == nil {
		return ErrNoClipboardTool
	}
	err := c.Copy(ctx, CaptureSnippet)
	if errors.Is(err, clipboard.ErrNoTool) {
		return ErrNoClipboardTool
	}
	return err
}
