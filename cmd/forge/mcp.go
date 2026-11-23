package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/mcpserver"
	"github.com/daan3011/forge/internal/packs"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
)

// mcpLogMax is the size at which mcp.log is rotated to mcp.log.1.
const mcpLogMax = 1 << 20

func newMCPCmd(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run forge's MCP server over stdio (started by Claude Code in forge sessions)",
		Long: `Serves forge's MCP tools over stdio for the Claude Code agent of a forge
session: Plane issues (filed as proposals you triage on Home), project docs
in .forge/docs, artifacts, session summary and progress, and context packs.

The session comes from FORGE_SESSION. forge passes the server to claude with
--mcp-config when it starts or resumes a session; you don't run this by hand.
Stdout carries the protocol only; logs go to mcp.log in forge's state folder.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true, // logged instead: stderr is shown in claude's MCP log
		RunE: func(cmd *cobra.Command, _ []string) error {
			return d.runMCP(cmd.Context())
		},
	}
}

// mcpLogPath is <state home>/forge/mcp.log.
func mcpLogPath() string { return filepath.Join(config.StateHome(), "forge", "mcp.log") }

// runMCP serves until the client hangs up. All logging, including the
// standard logger and panics, goes to mcp.log so stdout stays clean.
func (d deps) runMCP(ctx context.Context) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	logw, closeLog := openMCPLog()
	defer closeLog()
	log.SetOutput(logw)
	logger := slog.New(slog.NewTextHandler(logw, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("forge mcp: panic: %v", r)
		}
		if err != nil {
			logger.Error("forge mcp stopped", "err", err)
			_, _ = fmt.Fprintln(os.Stderr, "forge mcp:", err)
		}
	}()

	srv, closeAll, err := d.mcpServer(ctx, logger)
	if err != nil {
		return err
	}
	defer closeAll()
	if x := srv.Session(); x != nil {
		logger.Info("forge mcp serving", "session", x.Name, "project", x.Project)
	} else {
		logger.Warn("forge mcp serving without a session", "FORGE_SESSION", d.env(agent.EnvSessionName))
	}
	var t mcp.Transport = &mcp.StdioTransport{}
	if d.mcpTransport != nil {
		t = d.mcpTransport()
	}
	return srv.Run(ctx, t)
}

// mcpServer opens the store, resolves projects and Plane, and builds the
// server. Plane problems don't stop it: Plane tools report them.
func (d deps) mcpServer(ctx context.Context, logger *slog.Logger) (*mcpserver.Server, func(), error) {
	cfg, err := d.hookConfig()
	if err != nil {
		logger.Warn("config", "err", err) // defaults are still usable
	}
	st, err := store.Open(ctx, d.dbPath())
	if err != nil {
		return nil, nil, err
	}
	projects.Resolve(ctx, cfg, st.Projects(), d.cwd(), d.home(), logWriter{logger})
	var (
		pc   plane.API
		perr error
	)
	pc, perr = d.planeClient(cfg.Plane, st.Cache())
	if perr != nil {
		pc = nil
		logger.Info("plane unavailable", "err", perr)
	}
	packDir, _ := packs.Dir()
	sender := d.liveSender()
	srv, err := mcpserver.New(ctx, mcpserver.Deps{
		Store: st, Plane: pc, PlaneErr: perr, Config: cfg, GitHub: githubPulls,
		SessionName: d.env(agent.EnvSessionName), Cwd: d.cwd(),
		PacksDir: packDir, Version: version(), Now: d.clock, Logger: logger,
		Ping: func(ctx context.Context, p live.Ping) {
			if _, err := sender.Send(ctx, p); err != nil {
				logger.Debug("ping", "err", err)
			}
		},
	})
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return srv, func() { _ = st.Close() }, nil
}

// openMCPLog opens mcp.log for appending, rotating it past mcpLogMax. If it
// can't, logs go to stderr (Claude Code keeps an MCP server's stderr), never
// stdout.
func openMCPLog() (io.Writer, func()) {
	path := mcpLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return os.Stderr, func() {}
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > mcpLogMax {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return os.Stderr, func() {}
	}
	return f, func() { _ = f.Close() }
}

// logWriter adapts warnings written to an io.Writer to the logger.
type logWriter struct{ l *slog.Logger }

func (w logWriter) Write(p []byte) (int, error) {
	w.l.Warn(string(p))
	return len(p), nil
}

// version is the module version forge was built at, "dev" otherwise.
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
