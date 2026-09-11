// Command limelit is the whole product: the dashboard, the MCP server, the
// evaluation runner and the export, in one binary with no runtime
// dependencies.
//
//	limelit serve     UI + JSON API + MCP over HTTP + scheduler
//	limelit mcp       MCP over stdio, for a local Claude
//	limelit run       one evaluation pass, for cron
//	limelit export    JSON or CSV of everything
//	limelit upgrade   move to Limelit Cloud
//	limelit version   version and commit
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/limelitgeo/open/internal/config"
	"github.com/limelitgeo/open/internal/httpx"
	"github.com/limelitgeo/open/internal/store"
)

// version is stamped at build time with -ldflags; it falls back to the module
// build info so `go install` still reports something truthful.
var version = ""

const usage = `limelit - self-hosted AI visibility tracking

Usage:
  limelit <command> [flags]

Commands:
  serve     run the dashboard, JSON API, MCP endpoint and scheduler
  mcp       run the MCP server over stdio (for Claude Desktop / Claude Code)
  run       run one evaluation pass and exit (for cron)
  export    write everything this instance knows to stdout
  upgrade   move this instance to Limelit Cloud
  version   print version and build info

Run "limelit <command> -h" for the flags of one command.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "limelit: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	// Ctrl-C and SIGTERM cancel the root context. Every command below either
	// returns promptly on cancellation or drains first; nothing is killed
	// mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(ctx, rest)
	case "mcp":
		return cmdMCP(ctx, rest)
	case "run":
		return cmdRun(ctx, rest)
	case "export":
		return cmdExport(ctx, rest)
	case "upgrade":
		return cmdUpgrade(ctx, rest)
	case "version":
		fmt.Println(buildVersion())
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":1515", "listen address")
	cfgPath := fs.String("config", "limelit.yaml", "path to limelit.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := newLogger()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, config.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()

	if cfg.Configured() {
		log.Info("configuration loaded",
			"property", cfg.Property.Name,
			"competitors", len(cfg.Competitors),
			"targets", len(cfg.Targets),
			"runs_per_day", cfg.Limits.RunsPerDay,
			"schedule", cfg.Schedule)
	} else {
		// Not an error. A fresh instance boots into the setup wizard; that is
		// the whole point of the first-ten-minutes claim in the README.
		log.Info("not configured yet, the setup wizard will run at the dashboard", "config", *cfgPath)
	}

	srv := httpx.New(*addr, db, log, buildVersion())
	log.Info("listening", "addr", srv.Addr(), "database", db.Path(), "version", buildVersion())
	if err := srv.Serve(ctx); err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

// cmdMCP will serve the tool catalog in docs/tools.md over stdio.
func cmdMCP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(ctx, config.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	return errNotImplemented("mcp")
}

// cmdRun will execute one evaluation pass across every enabled target.
func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.String("target", "", "run only this target (engine:provider[:model][:online])")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(ctx, config.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	return errNotImplemented("run")
}

// cmdExport will write the payload that `upgrade` also sends.
func cmdExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.String("format", "json", "json or csv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(ctx, config.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	return errNotImplemented("export")
}

// cmdUpgrade will move this instance to Limelit Cloud.
func cmdUpgrade(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(ctx, config.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	return errNotImplemented("upgrade")
}

// errNotImplemented names the command and points at where it lands, so a
// scaffold build reports a gap instead of pretending to work.
func errNotImplemented(cmd string) error {
	return errors.New(cmd + " is not implemented yet, see https://github.com/limelitgeo/open/issues")
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("LIMELIT_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	revision, modified := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	switch {
	case revision == "":
		return "dev"
	case modified:
		return revision[:min(len(revision), 12)] + "-dirty"
	default:
		return revision[:min(len(revision), 12)]
	}
}
