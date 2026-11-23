// Package notes is the daily note (P6.2): one Markdown file per day under
// the notes directory, and the standup section built from the day's work.
package notes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dateLayout names a note file.
const dateLayout = "2006-01-02"

// Day is local midnight of t's day.
func Day(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// AddDays moves day by n calendar days, staying at midnight across DST.
func AddDays(day time.Time, n int) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d+n, 0, 0, 0, 0, day.Location())
}

// PrevWorkday is the weekday before day: Friday for a Saturday, Sunday or
// Monday.
func PrevWorkday(day time.Time) time.Time {
	p := AddDays(day, -1)
	for p.Weekday() == time.Saturday || p.Weekday() == time.Sunday {
		p = AddDays(p, -1)
	}
	return p
}

// Path is day's note file in dir.
func Path(dir string, day time.Time) string {
	return filepath.Join(dir, day.Format(dateLayout)+".md")
}

// Store reads and writes the notes in Dir.
type Store struct {
	Dir string
}

// Path is day's note file.
func (s Store) Path(day time.Time) string { return Path(s.Dir, day) }

// Read returns day's note; a day without a note reads as "".
func (s Store) Read(day time.Time) (string, error) {
	b, err := os.ReadFile(s.Path(day))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("notes: %w", err)
	}
	return string(b), nil
}

// Write replaces day's note. The file is written next to the old one and
// renamed over it, so a crash never leaves half a note.
func (s Store) Write(day time.Time, text string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("notes: %w", err)
	}
	f, err := os.CreateTemp(s.Dir, ".note-*")
	if err != nil {
		return fmt.Errorf("notes: %w", err)
	}
	tmp := f.Name()
	_, werr := f.WriteString(text)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("notes: %w", err)
	}
	if err := os.Rename(tmp, s.Path(day)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("notes: %w", err)
	}
	return nil
}

// Append adds section to the end of day's note, after a blank line, and
// returns the new note.
func (s Store) Append(day time.Time, section string) (string, error) {
	old, err := s.Read(day)
	if err != nil {
		return "", err
	}
	text := Join(old, section)
	return text, s.Write(day, text)
}

// Join appends section to text with one blank line between them.
func Join(text, section string) string {
	section = strings.TrimRight(section, "\n") + "\n"
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return section
	}
	return text + "\n\n" + section
}
