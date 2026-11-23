package app

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/ui"
)

// DoctorFunc runs `forge doctor`'s checks and returns the plain report
// and whether any check failed.
type DoctorFunc func(ctx context.Context) (report string, failed bool)

// WithDoctor adds the "Run doctor" palette command (P6.4).
func WithDoctor(f DoctorFunc) Option {
	return func(m *Model) { m.doctor = f }
}

// doctorTimeout bounds a palette doctor run (each check has its own 5s).
const doctorTimeout = 30 * time.Second

// doctorDoneMsg carries a finished doctor run.
type doctorDoneMsg struct{ run ui.CommandRun }

// doctorItems is the palette entry, when a doctor is wired.
func (m Model) doctorItems() []item {
	if m.doctor == nil {
		return nil
	}
	f := m.doctor
	return []item{{
		id: "doctor", title: "Run doctor", group: groupCommands,
		detail: "check tools, logins and session registrations",
		run: func(m Model) (Model, tea.Cmd) {
			return m, tea.Batch(m.toast.Show("running forge doctor…", ui.ToastInfo), func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
				defer cancel()
				start := time.Now()
				report, failed := f(ctx)
				dir, _ := os.Getwd()
				r := ui.CommandRun{Name: "doctor", Command: "forge doctor", Dir: dir, Started: start, Took: time.Since(start), Output: report}
				if failed {
					r.Exit = 1
				}
				return doctorDoneMsg{run: r}
			})
		},
	}}
}
