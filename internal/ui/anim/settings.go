package anim

import "github.com/daan3011/forge/internal/config"

// Environment variables that turn animation down.
const (
	// EnvNoAnim set to anything turns every animation off.
	EnvNoAnim = "FORGE_NO_ANIM"
	// EnvNoColor is the https://no-color.org convention. Colour itself is
	// stripped by the terminal renderer; here it only stops colour-only
	// pulses from ticking, since nobody would see them.
	EnvNoColor = "NO_COLOR"
)

// Settings is the resolved animation level for this run.
type Settings struct {
	Level config.Animations
	Color bool // false under NO_COLOR
}

// Resolve combines the configured level with the environment. getenv is
// os.Getenv outside tests.
func Resolve(level config.Animations, getenv func(string) string) Settings {
	switch level {
	case config.AnimationsFull, config.AnimationsReduced, config.AnimationsOff:
	default:
		level = config.AnimationsFull
	}
	s := Settings{Level: level, Color: true}
	if getenv == nil {
		return s
	}
	if getenv(EnvNoAnim) != "" {
		s.Level = config.AnimationsOff
	}
	if getenv(EnvNoColor) != "" {
		s.Color = false
	}
	return s
}

// Full returns full animation with colour, for tests and the demo.
func Full() Settings { return Settings{Level: config.AnimationsFull, Color: true} }

// Motion reports whether glyphs may move (full only).
func (s Settings) Motion() bool { return s.Level == config.AnimationsFull }

// Pulses reports whether colours may change over time (full or reduced, and
// colour is shown).
func (s Settings) Pulses() bool { return s.Level != config.AnimationsOff && s.Color }

// Off reports whether animation is off entirely.
func (s Settings) Off() bool { return s.Level == config.AnimationsOff }
