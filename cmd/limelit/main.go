// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/limelitgeo/open/internal/config"
	"github.com/limelitgeo/open/internal/credentials"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/limelitgeo/open/internal/httpx"
	"github.com/limelitgeo/open/internal/mcpserver"
	"github.com/limelitgeo/open/internal/provider"
	"github.com/limelitgeo/open/internal/runner"
	"github.com/limelitgeo/open/internal/secrets"
	"github.com/limelitgeo/open/internal/store"
	"github.com/limelitgeo/open/internal/target"
	"github.com/limelitgeo/open/internal/ui"
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

	registry := provider.Default()
	if cfg.Configured() {
		log.Info("configuration loaded",
			"property", cfg.Property.Name,
			"competitors", len(cfg.Competitors),
			"targets", len(cfg.Targets),
			"runs_per_day", cfg.Limits.RunsPerDay,
			"schedule", cfg.Schedule)
		// Targets are checked here, at startup, against the providers that
		// are actually compiled in. A typo belongs in the log on the line
		// after "configuration loaded", not three hours later in the middle
		// of a scheduled run. It is a warning rather than a fatal error
		// because the dashboard has to come up so the target can be fixed.
		if targets, err := target.ParseAll(cfg.Targets, registry); err != nil {
			log.Warn("some targets will not run", "error", err)
		} else {
			log.Info("targets resolved", "count", len(targets))
		}
	} else {
		// Not an error. A fresh instance boots into the setup wizard; that is
		// the whole point of the first-ten-minutes claim in the README.
		log.Info("not configured yet, the setup wizard will run at the dashboard", "config", *cfgPath)
	}
	if len(registry.Names()) == 0 {
		log.Warn("no providers are compiled in yet, so no target can run")
	}

	keys, err := secrets.Open(config.DataDir())
	if err != nil {
		return err
	}

	// A pass can only be advanced by the process that started it, so one left
	// running by a crash would show as in flight forever.
	if n, err := db.SweepOrphanedEvaluations(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("closed evaluations left running by an earlier process", "count", n)
	}

	run := runner.New(db, registry, credentials.Source(ctx, db, keys, log), log)
	run.NewAnalyzer = func(c context.Context) (runner.Analyzer, error) { return runner.NewStoreAnalyzer(c, db) }
	dash, err := ui.New(db, registry, keys, run, log, buildVersion(), cfg)
	if err != nil {
		return err
	}

	stopSchedule := startSchedule(ctx, cfg, run, db, log)
	defer stopSchedule()

	srv := httpx.New(*addr, db, log, buildVersion(), dash)
	log.Info("listening", "addr", srv.Addr(), "database", db.Path(), "version", buildVersion())
	if err := srv.Serve(ctx); err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

// startSchedule runs a pass on an interval.
//
// Two intervals rather than a cron expression, because cron would mean a
// dependency and a syntax to learn for a choice that is really "how often".
// Anything else is `limelit run` from the system's own cron, which is also
// the only way to schedule when the dashboard is not running.
func startSchedule(ctx context.Context, cfg *config.Config, run *runner.Runner, db *store.DB, log *slog.Logger) func() {
	var every time.Duration
	switch strings.ToLower(strings.TrimSpace(cfg.Schedule)) {
	case "daily":
		every = 24 * time.Hour
	case "hourly":
		every = time.Hour
	case "", "off":
		return func() {}
	default:
		log.Warn("unknown schedule, nothing will run automatically",
			"schedule", cfg.Schedule, "supported", "daily, hourly, off")
		return func() {}
	}

	log.Info("scheduling automatic runs", "every", every)
	ticker := time.NewTicker(every)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ceiling := cfg.Limits.RunsPerDay
				if stored, err := db.Setting(ctx, "runs_per_day"); err == nil && stored != "" {
					if n, err := strconv.Atoi(stored); err == nil && n > 0 {
						ceiling = n
					}
				}
				res, err := run.Run(ctx, runner.Options{RunsPerDay: ceiling})
				if err != nil {
					log.Error("scheduled run failed", "error", err)
					continue
				}
				log.Info("scheduled run finished", "evaluation", res.EvaluationID,
					"completed", res.Completed, "failed", res.Failed)
			}
		}
	}()
	return func() { ticker.Stop() }
}

// cmdMCP serves the tool catalog over stdio, which is the shape Claude
// Desktop and most MCP clients launch.
//
// Nothing is written to stdout except protocol frames: stdout IS the
// transport, so a stray Println would corrupt the session. Logs go to stderr.
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

	srv, err := mcpserver.New(mcpserver.Deps{DB: db})
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// cmdRun executes one evaluation pass and exits, which is the shape a system
// cron wants.
func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	targetSpec := fs.String("target", "", "run only this target (engine:provider[:model][:online])")
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

	keys, err := secrets.Open(config.DataDir())
	if err != nil {
		return err
	}
	if _, err := db.SweepOrphanedEvaluations(ctx); err != nil {
		return err
	}

	ceiling := cfg.Limits.RunsPerDay
	if stored, err := db.Setting(ctx, "runs_per_day"); err == nil && stored != "" {
		if n, err := strconv.Atoi(stored); err == nil && n > 0 {
			ceiling = n
		}
	}

	run := runner.New(db, provider.Default(), credentials.Source(ctx, db, keys, log), log)
	run.NewAnalyzer = func(c context.Context) (runner.Analyzer, error) { return runner.NewStoreAnalyzer(c, db) }
	res, err := run.Run(ctx, runner.Options{TargetSpec: *targetSpec, RunsPerDay: ceiling})
	if err != nil {
		return err
	}
	fmt.Printf("evaluation %d: %d of %d answers, %d failed, in %s\n",
		res.EvaluationID, res.Completed, res.Planned, res.Failed, res.Duration.Round(time.Second))
	if res.Failed > 0 {
		// A cron job whose provider key expired should not report success.
		return fmt.Errorf("%d of %d answers failed", res.Failed, res.Planned)
	}
	return nil
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
