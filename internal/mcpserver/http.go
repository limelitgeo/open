// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package mcpserver

// MCP over streamable HTTP, for clients that connect to a running instance
// rather than launching one.

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TokenEnv is the environment variable holding the bearer token.
const TokenEnv = "LIMELIT_MCP_TOKEN"

// TokenSource returns the bearer token in force right now, or "" when there
// is none. It is a function rather than a string so a token generated or
// rotated in Settings takes effect on the next request instead of the next
// restart; a Rotate button that needed a restart would leave the old token
// working while the screen said it was gone.
type TokenSource func() string

// StaticToken is a TokenSource for a token fixed at startup.
func StaticToken(token string) TokenSource { return func() string { return token } }

// Handler serves MCP over streamable HTTP.
//
// A token is REQUIRED. An unauthenticated endpoint here would hand anyone who
// can reach the port every answer the instance has stored, and a self-hosted
// tool is frequently put on a box with a more generous firewall than its
// author assumed. With no token configured the handler refuses every request
// and says how to set one, which fails closed and explains itself.
func Handler(srv *mcp.Server, source TokenSource, log *slog.Logger) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	if source == nil {
		source = StaticToken("")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(source())
		if token == "" {
			http.Error(w,
				"MCP over HTTP is disabled because no token is set. Generate one in Settings, or set "+TokenEnv+
					" and restart, then send it as Authorization: Bearer <token>. "+
					"Or use the stdio transport with `limelit mcp`, which needs no token.",
				http.StatusServiceUnavailable)
			return
		}
		if !authorized(r, token) {
			// The challenge names the scheme so a client knows what to send
			// back rather than guessing.
			w.Header().Set("WWW-Authenticate", `Bearer realm="limelit"`)
			http.Error(w, "a bearer token is required", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r)
	})
}

// authorized compares in constant time, so the endpoint does not leak the
// token one byte at a time to anyone willing to measure.
func authorized(r *http.Request, token string) bool {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	given := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(given), []byte(token)) == 1
}
