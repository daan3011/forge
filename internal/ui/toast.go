package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ErrMsg reports an error from a command. The root model shows it as a toast.
type ErrMsg struct{ Err error }

// InfoMsg reports a short status message. The root model shows it as a toast
// and in the status bar.
type InfoMsg struct{ Text string }

// SuccessMsg reports that an action worked (merged, approved, created,
// started). The root model shows it as a success toast with a spark burst.
type SuccessMsg struct{ Text string }

// ToastLevel is the severity of a toast.
type ToastLevel int

// Toast levels.
const (
	ToastInfo ToastLevel = iota
	ToastError
	ToastSuccess
)

// SuccessMark prefixes success toasts; it is the static stand-in for the
// spark burst when animations are reduced or off.
const SuccessMark = "✦ "

// ToastDuration is how long a toast stays on screen.
const ToastDuration = 4 * time.Second

// maxToasts is how many toasts are stacked at once; older ones drop off.
const maxToasts = 3

type toastItem struct {
	id    int
	text  string
	level ToastLevel
}

// toastExpireMsg removes the toast with the given id.
type toastExpireMsg struct{ id int }

// Toast is a stack of short-lived notifications.
type Toast struct {
	theme  *Theme
	items  []toastItem
	nextID int
}

// NewToast returns an empty toast stack.
func NewToast(theme *Theme) Toast { return Toast{theme: theme} }

// Show adds a toast and returns the command that expires it.
func (t *Toast) Show(text string, level ToastLevel) tea.Cmd {
	t.nextID++
	id := t.nextID
	t.items = append(t.items, toastItem{id: id, text: text, level: level})
	if len(t.items) > maxToasts {
		t.items = t.items[len(t.items)-maxToasts:]
	}
	return tea.Tick(ToastDuration, func(time.Time) tea.Msg { return toastExpireMsg{id: id} })
}

// Update handles expiry messages. It reports whether msg was a toast message.
func (t *Toast) Update(msg tea.Msg) bool {
	m, ok := msg.(toastExpireMsg)
	if !ok {
		return false
	}
	for i, it := range t.items {
		if it.id == m.id {
			t.items = append(t.items[:i], t.items[i+1:]...)
			break
		}
	}
	return true
}

// Empty reports whether no toast is showing.
func (t *Toast) Empty() bool { return len(t.items) == 0 }

// View renders the stacked toasts, each at most maxWidth cells wide.
func (t *Toast) View(maxWidth int) string {
	if len(t.items) == 0 || maxWidth < 8 {
		return ""
	}
	boxes := make([]string, 0, len(t.items))
	for _, it := range t.items {
		boxes = append(boxes, t.box(it, maxWidth))
	}
	return lipgloss.JoinVertical(lipgloss.Right, boxes...)
}

// LastSize returns the size of the newest (bottom) toast box as View draws
// it, or zeros when no toast is showing.
func (t *Toast) LastSize(maxWidth int) (width, height int) {
	if len(t.items) == 0 || maxWidth < 8 {
		return 0, 0
	}
	b := t.box(t.items[len(t.items)-1], maxWidth)
	return lipgloss.Width(b), lipgloss.Height(b)
}

func (t *Toast) box(it toastItem, maxWidth int) string {
	style := t.theme.Toast
	text := it.text
	switch it.level {
	case ToastError:
		style = t.theme.ToastError
	case ToastSuccess:
		style = t.theme.ToastSuccess
		text = SuccessMark + text
	}
	inner := maxWidth - style.GetHorizontalFrameSize()
	text = lipgloss.NewStyle().Width(min(lipgloss.Width(text), inner)).Render(text)
	return style.Render(text)
}
