package notes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }

func TestPrevWorkday(t *testing.T) {
	tests := []struct {
		day, want time.Time
	}{
		{date(2026, 11, 23), date(2026, 11, 20)}, // Mon → Fri
		{date(2026, 11, 22), date(2026, 11, 20)}, // Sun → Fri
		{date(2026, 11, 21), date(2026, 11, 20)}, // Sat → Fri
		{date(2026, 11, 24), date(2026, 11, 23)}, // Tue → Mon
		{date(2026, 1, 1), date(2025, 12, 31)},   // across a year
	}
	for _, tt := range tests {
		if got := PrevWorkday(tt.day); !got.Equal(tt.want) {
			t.Errorf("PrevWorkday(%s) = %s, want %s", tt.day.Format("Mon 2006-01-02"), got, tt.want)
		}
	}
}

func TestDaysAcrossDST(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	// Clocks go back on Sun 25 Oct 2026; that day has 25 hours.
	mon := time.Date(2026, 10, 26, 0, 0, 0, 0, ams)
	if got := PrevWorkday(mon); got.Day() != 23 || got.Hour() != 0 {
		t.Errorf("PrevWorkday across DST = %s", got)
	}
	if got := AddDays(time.Date(2026, 10, 25, 0, 0, 0, 0, ams), 1); got.Day() != 26 || got.Hour() != 0 {
		t.Errorf("AddDays across DST = %s", got)
	}
}

func TestDay(t *testing.T) {
	got := Day(time.Date(2026, 11, 23, 23, 59, 0, 0, time.Local))
	if !got.Equal(date(2026, 11, 23)) {
		t.Errorf("Day = %s", got)
	}
}

func TestStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	s := Store{Dir: dir}
	day := date(2026, 11, 23)
	if got := s.Path(day); got != filepath.Join(dir, "2026-11-23.md") {
		t.Errorf("Path = %s", got)
	}
	text, err := s.Read(day)
	if err != nil || text != "" {
		t.Fatalf("Read missing = %q, %v", text, err)
	}
	if err := s.Write(day, "todo: ship P6.2\n\n\n"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Append(day, "## Standup\n- x")
	if err != nil {
		t.Fatal(err)
	}
	want := "todo: ship P6.2\n\n## Standup\n- x\n"
	if got != want {
		t.Errorf("Append = %q, want %q", got, want)
	}
	if text, _ := s.Read(day); text != want {
		t.Errorf("Read after Append = %q", text)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want just the note (no temp files)", len(entries))
	}
	// Appending to a day without a note starts it.
	if got, _ := s.Append(date(2026, 11, 24), "## S\n"); got != "## S\n" {
		t.Errorf("Append to new note = %q", got)
	}
}

func TestJoin(t *testing.T) {
	tests := []struct{ text, section, want string }{
		{"", "a", "a\n"},
		{"x", "a\n\n", "x\n\na\n"},
		{"x\n", "a", "x\n\na\n"},
		{"\n\n", "a", "a\n"},
	}
	for _, tt := range tests {
		if got := Join(tt.text, tt.section); got != tt.want {
			t.Errorf("Join(%q, %q) = %q, want %q", tt.text, tt.section, got, tt.want)
		}
	}
}
