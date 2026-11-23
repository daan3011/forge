package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/rsvg"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui/termcaps"
)

// WithCapsDetector detects terminal graphics at startup for inline
// previews (P4.7). Without it tabs never hear about graphics, so previews
// stay text only.
func WithCapsDetector(d *termcaps.Detector) Option {
	return func(m *Model) { m.capsDet = d }
}

// WithPreviewTools sets the external tools image previews use: chafa for
// text art and rsvg-convert for SVGs (P4.7). Either may be nil.
func WithPreviewTools(c chafa.Renderer, r rsvg.Rasterizer) Option {
	return func(m *Model) { m.chafa, m.rsvg = c, r }
}

// graphicsAware returns t as a tabs.GraphicsAware, if it is one.
func graphicsAware(t tabs.Tab) (tabs.GraphicsAware, bool) {
	g, ok := t.(tabs.GraphicsAware)
	return g, ok
}

// updateCaps feeds msg to the detector. handled means msg was its
// message; when detection gives a new result every graphics tab hears.
func (m Model) updateCaps(msg tea.Msg) (Model, tea.Cmd, bool) {
	if m.capsDet == nil {
		return m, nil, false
	}
	caps, changed, handled := m.capsDet.Update(msg)
	if !handled || !changed {
		return m, nil, handled
	}
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if g, ok := graphicsAware(t); ok {
			cmds = append(cmds, g.SetCaps(caps))
		}
	}
	return m, tea.Batch(cmds...), true
}

// resized tells graphics tabs about a new window size (after SetSize) and
// asks the terminal for its cell size again.
func (m Model) resized() tea.Cmd {
	var cmds []tea.Cmd
	if m.capsDet != nil {
		cmds = append(cmds, m.capsDet.Resized())
	}
	for _, t := range m.tabs {
		if g, ok := graphicsAware(t); ok {
			cmds = append(cmds, g.Resized())
		}
	}
	return tea.Batch(cmds...)
}

// occludedNow reports whether anything is drawn over the active tab: the
// splash, help, command output, the palette or quick capture, or a toast.
func (m Model) occludedNow() bool {
	return m.splash != nil || m.helpOpen || m.outputOpen || m.overlay != overlayNone || !m.toast.Empty()
}

// syncOccluded tells the active tab when something starts or stops
// covering it (sixel images must be cleared and redrawn around overlays).
func (m Model) syncOccluded() (Model, tea.Cmd) {
	occ := m.occludedNow()
	if occ == m.occluded {
		return m, nil
	}
	m.occluded = occ
	if len(m.tabs) == 0 {
		return m, nil
	}
	if g, ok := graphicsAware(m.tabs[m.active]); ok {
		return m, g.SetOccluded(occ)
	}
	return m, nil
}
