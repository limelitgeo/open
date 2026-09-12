// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

// Package ui is the embedded dashboard: Go templates and a stylesheet
// compiled into the binary, served by `limelit serve`.
//
// There is no JavaScript build step and no Node in this repository. Every
// screen is a server-rendered form, which is also why the setup wizard works
// with JavaScript disabled: a GTM user's first three minutes should not
// depend on a bundle loading.
//
// Templates are parsed once at construction so a broken template is a startup
// failure, not a 500 the first time someone opens that page.
package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// pages maps a page template to the chrome it renders inside. Every page
// template defines "page"; the chrome defines the document around it.
var pages = map[string]string{
	"overview":           "layout",
	"chats":              "layout",
	"chat":               "layout",
	"citations":          "layout",
	"prompts":            "layout",
	"competitors":        "layout",
	"settings":           "layout",
	"upgrade":            "layout",
	"wizard_brand":       "wizard",
	"wizard_competitors": "wizard",
	"wizard_prompts":     "wizard",
	"wizard_provider":    "wizard",
}

// Renderer holds the parsed templates.
type Renderer struct {
	// One template set per page, because every page defines a block named
	// "page" and they would otherwise overwrite each other.
	sets map[string]*template.Template
}

// NewRenderer parses every template. It returns an error rather than panicking
// so the caller can report it with the rest of startup.
func NewRenderer() (*Renderer, error) {
	r := &Renderer{sets: make(map[string]*template.Template, len(pages))}
	for page, chrome := range pages {
		t, err := template.New(chrome+".html").Funcs(funcs()).ParseFS(
			templateFS,
			"templates/"+chrome+".html",
			"templates/"+page+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		r.sets[page] = t
	}
	return r, nil
}

// Render writes one page.
func (r *Renderer) Render(w io.Writer, page string, data any) error {
	t, ok := r.sets[page]
	if !ok {
		return fmt.Errorf("unknown page %q", page)
	}
	chrome := pages[page]
	return t.ExecuteTemplate(w, chrome, data)
}

// StaticHandler serves the embedded stylesheet.
func StaticHandler() http.Handler {
	return http.FileServer(http.FS(staticFS))
}

func funcs() template.FuncMap {
	return template.FuncMap{
		"join": strings.Join,
	}
}
