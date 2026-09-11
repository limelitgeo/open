// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

// Package httpx is the HTTP surface: the dashboard, the JSON API and the MCP
// endpoint all hang off one mux so a number is served the same way whoever
// asks for it.
//
// In this scaffold only /healthz is real. The UI (#20 to #23), the JSON API
// and the MCP endpoint (#18) land on this same router.
package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/limelitgeo/open/internal/store"
	"github.com/limelitgeo/open/internal/ui"
)

// Server is the HTTP listener and its dependencies.
type Server struct {
	db      *store.DB
	log     *slog.Logger
	version string
	http    *http.Server
}

// New builds a Server bound to addr. dash may be nil, which serves the health
// endpoint alone; that is the shape the tests use.
func New(addr string, db *store.DB, log *slog.Logger, version string, dash *ui.App) *Server {
	s := &Server{db: db, log: log, version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	if dash != nil {
		dash.Routes(mux)
	}
	s.http = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Addr is the configured listen address.
func (s *Server) Addr() string { return s.http.Addr }

// Serve listens until ctx is cancelled, then drains in-flight requests.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

type health struct {
	Status     string   `json:"status"`
	Version    string   `json:"version"`
	Database   string   `json:"database"`
	Migrations []string `json:"migrations"`
}

// handleHealthz reports that the process is up AND that the database it
// depends on answers. A health check that only proves the listener is alive
// is the kind that stays green through an outage.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	body := health{Status: "ok", Version: s.version, Database: s.db.Path()}
	migrations, err := s.db.AppliedMigrations(ctx)
	if err != nil {
		s.log.Error("healthz database check failed", "error", err)
		body.Status = "degraded"
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	body.Migrations = migrations
	writeJSON(w, http.StatusOK, body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
