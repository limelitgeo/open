// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

//go:build live

package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Live tests call the real API and cost real money. They are behind a build
// tag so `go test ./...` never spends anything:
//
//	OPENAI_API_KEY=sk-... go test -tags live ./internal/provider/ -run Live -v
//
// They exist because the offline fixtures can only prove this code reads the
// shape OpenAI returned on the day it was recorded. Only a live call proves
// the model alias still resolves, which is the failure mode that takes a
// provider down without any code changing.
func TestOpenAILiveAnswersWithCitations(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	p, err := NewOpenAI(key)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := p.Run(ctx, Request{
		Engine: ChatGPTEngine,
		Prompt: "What are the best AI visibility tracking tools?",
		Online: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(resp.Text) < 200 {
		t.Errorf("answer is %d characters", len(resp.Text))
	}
	if len(resp.Citations) == 0 {
		t.Error("a web-grounded answer returned no citations, so either the tool did not run or the annotations moved")
	}
	if resp.InputTokens == 0 {
		t.Error("no usage was reported")
	}
	t.Logf("model=%s citations=%d tokens=%d/%d", resp.Model, len(resp.Citations), resp.InputTokens, resp.OutputTokens)
}

// TestOpenAILiveDefaultModelStillResolves is the one that matters between
// releases: OpenAI retires aliases, and a 404 here means every ChatGPT target
// in every install has stopped working.
func TestOpenAILiveDefaultModelStillResolves(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	p, err := NewOpenAI(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := p.Run(ctx, Request{Engine: ChatGPTEngine, Prompt: "Say OK."})
	if err != nil {
		t.Fatalf("the default model %q no longer resolves: %v", OpenAIDefaultModel, err)
	}
	t.Logf("%q resolved to %q", OpenAIDefaultModel, resp.Model)
}

func TestOpenAILiveTestRejectsABadKey(t *testing.T) {
	p, err := NewOpenAI("sk-definitely-not-a-real-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Test(context.Background()); err == nil {
		t.Error("a nonsense key passed Test")
	}
}
