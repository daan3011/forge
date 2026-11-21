package capture

import (
	"errors"

	"github.com/daan3011/forge/internal/ui"
)

// Completer adapts a catalog to the capture line's ui.CaptureCompleter.
type Completer struct {
	Catalog        Catalog
	DefaultProject string
}

var _ ui.CaptureCompleter = Completer{}

// Complete implements ui.CaptureCompleter.
func (c Completer) Complete(text string, cursor int) (ui.Completion, bool) {
	comp, ok := c.Catalog.Complete(text, cursor, c.DefaultProject)
	if !ok {
		return ui.Completion{}, false
	}
	return ui.Completion{Start: comp.Start, End: comp.End, Sigil: comp.Kind.Sigil(), Candidates: comp.Candidates}, true
}

// FixOf is the capture line's fix for err: the "did you mean" edit of an
// *UnknownError, else nil.
func FixOf(err error) *ui.CaptureFix {
	var ue *UnknownError
	if !errors.As(err, &ue) {
		return nil
	}
	start, end, text, ok := ue.Fix()
	if !ok {
		return nil
	}
	return &ui.CaptureFix{Start: start, End: end, Text: text}
}
