package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultTemplate []byte

// Template returns the commented default config written on first run.
func Template() []byte { return append([]byte(nil), defaultTemplate...) }

// EnvConfigDir and EnvDataDir point forge's own config and data
// directories somewhere else. They are checked before the XDG variables and
// only affect forge, so a clean test install never changes where claude (or
// anything else) keeps its files.
const (
	EnvConfigDir = "FORGE_CONFIG_DIR"
	EnvDataDir   = "FORGE_DATA_DIR"
)

// Dir returns forge's config directory: $FORGE_CONFIG_DIR, else
// $XDG_CONFIG_HOME/forge, else ~/.config/forge. It deliberately ignores
// os.UserConfigDir, which points at ~/Library/Application Support on macOS.
func Dir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: find home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "forge"), nil
}

// Path returns the config file location: Dir()/config.yaml.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// TmuxConfPath returns the config file of forge's tmux server:
// Dir()/tmux.conf.
func TmuxConfPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tmux.conf"), nil
}

// EnsureExists writes the default template to path if no file is there yet.
// It reports whether it created the file.
func EnsureExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("config: %w", err)
	}
	if err := os.WriteFile(path, defaultTemplate, 0o644); err != nil { //nolint:gosec // config is not secret by default
		return false, fmt.Errorf("config: %w", err)
	}
	return true, nil
}

// LoadOrCreate creates the default config if needed and loads it. It returns
// the config, its path and whether the file was just created.
func LoadOrCreate() (cfg *Config, path string, created bool, err error) {
	path, err = Path()
	if err != nil {
		return nil, "", false, err
	}
	created, err = EnsureExists(path)
	if err != nil {
		return nil, path, false, err
	}
	cfg, err = Load(path)
	return cfg, path, created, err
}

// Load reads, defaults, expands and validates the config at path. Problems in
// the file are returned as Errors, each pointing at a line and key.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return parse(path, data)
}

var yamlLineRe = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

func parse(path string, data []byte) (*Config, error) {
	cfg := Default()

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		e := ValidationError{File: path, Msg: "invalid YAML: " + err.Error()}
		if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
			e.Line, _ = strconv.Atoi(m[1])
			e.Msg = "invalid YAML: " + m[2]
		}
		return nil, Errors{e}
	}
	if len(doc.Content) == 0 { // empty file: all defaults
		cfg.expand()
		return cfg, nil
	}

	idx, errs := indexNode(&doc)
	if err := doc.Decode(cfg); err != nil {
		var te *yaml.TypeError
		if !errors.As(err, &te) {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
		for _, msg := range te.Errors {
			e := ValidationError{Msg: msg}
			if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
				e.Line, _ = strconv.Atoi(m[1])
				e.Msg = m[2]
				e.Key = idx.keyAt[e.Line]
			}
			errs = append(errs, e)
		}
	}
	if len(errs) > 0 {
		return nil, errs.at(path)
	}

	cfg.expand()
	if err := cfg.Validate(); err != nil {
		var verrs Errors
		if errors.As(err, &verrs) {
			for i := range verrs {
				verrs[i].Line = idx.lineOf(verrs[i].Key)
			}
			return nil, verrs.at(path)
		}
		return nil, err
	}
	return cfg, nil
}
