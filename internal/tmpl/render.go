// Package tmpl loads the HTML templates and renders full pages (inside the
// layout) and htmx fragments (alone).
//
// Templates live under one directory:
//
//	layouts/*.html    parsed with every page; the page runs "base.html"
//	pages/*.html      each defines "content" (and may define "title", "head")
//	fragments/*.html  each parsed alone, no layout
//
// Everything goes through html/template, so values are escaped for where
// they appear (Security Rule 9).
package tmpl

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"meet.space/internal/branding"
)

// layoutName is the layout template a page is executed through.
const layoutName = "base.html"

// Site is what the layout needs to know about the site being shown.
type Site struct {
	Kind       string // "platform" or "group"
	SiteName   string // nav and default <title>: the platform name, or the group's name
	FooterText string // the platform name, or the group's name
	ThemeColor string // #rrggbb; anything else becomes the platform default (Rule 10)
	Look       string // clean, lively, confetti or poster; anything else becomes clean (Rule 10)
	Initials   string // group sites: shown in the brand tile when there is no logo
}

// Page is the data every full page is executed with.
//
// Title is for pages that define their own "title" block. The layout's
// default <title> is Site.SiteName.
// OPEN: how a page title combines with the site name in <title> (order,
// separator) is not in the documents.
type Page struct {
	Site    Site
	Title   string
	Content any
}

// ErrorContent is the Content of the "error" page and the data of the
// "error" fragment: one message and one way out (design.md 6,
// decisions.md "Error pages"). The caller supplies the words.
type ErrorContent struct {
	Status   int
	Message  string
	LinkText string
	LinkHref string
}

// AssetURLer turns a file name under static/ into its fingerprinted URL.
type AssetURLer interface {
	URL(name string) string
}

// Renderer renders the templates under one directory. It is safe for
// concurrent use.
type Renderer struct {
	dir    string
	funcs  template.FuncMap
	reload bool
	set    *templateSet // parsed once in New; unused when reload is set
}

type templateSet struct {
	pages     map[string]*template.Template
	fragments map[string]*template.Template
}

// New parses every page (with the layouts) and every fragment under dir.
// With reload set (development), Render and RenderFragment parse the
// files again on every call, so template edits show up without a restart.
func New(dir string, assets AssetURLer, reload bool) (*Renderer, error) {
	if assets == nil {
		return nil, errors.New("tmpl: assets is nil")
	}
	r := &Renderer{
		dir:    dir,
		funcs:  template.FuncMap{"asset": assets.URL},
		reload: reload,
	}
	set, err := r.parse()
	if err != nil {
		return nil, err
	}
	r.set = set
	return r, nil
}

// Pages returns the page names (file names without .html), sorted.
func (r *Renderer) Pages() []string { return names(r.dir, "pages") }

// Fragments returns the fragment names (file names without .html), sorted.
func (r *Renderer) Fragments() []string { return names(r.dir, "fragments") }

// Render writes the named page inside the layout.
//
// Rule 10 at the layout: before executing, Site.ThemeColor is replaced by
// the platform default (branding.DefaultBrand) unless it is a valid
// #rrggbb, and Site.Look by Clean unless it is one of the four looks. An
// invalid stored value is treated as no value, and a group with no color
// of its own uses the platform's default blue (development-plan.md, Base
// Layout: "A group without its own color gets a palette derived from the
// platform's default blue").
//
// The page is executed into a buffer first. On any error nothing is
// written to w (no header, no body) and the error is returned, so the
// caller can still send an error page. On success it sets Content-Type,
// writes status and the body. It never sets Cache-Control: the route
// layer owns that.
func (r *Renderer) Render(w http.ResponseWriter, status int, page string, p Page) error {
	set, err := r.current()
	if err != nil {
		return err
	}
	t, ok := set.pages[page]
	if !ok {
		return fmt.Errorf("tmpl: no page %q", page)
	}
	p.Site = safeSite(p.Site)
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, layoutName, p); err != nil {
		return fmt.Errorf("tmpl: page %q: %w", page, err)
	}
	write(w, status, buf.Bytes())
	return nil
}

// RenderFragment writes the named fragment alone, without the layout.
// Like Render, it writes nothing if anything fails, and never sets
// Cache-Control.
func (r *Renderer) RenderFragment(w http.ResponseWriter, status int, name string, data any) error {
	set, err := r.current()
	if err != nil {
		return err
	}
	t, ok := set.fragments[name]
	if !ok {
		return fmt.Errorf("tmpl: no fragment %q", name)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name+".html", data); err != nil {
		return fmt.Errorf("tmpl: fragment %q: %w", name, err)
	}
	write(w, status, buf.Bytes())
	return nil
}

// safeSite applies Rule 10 to the values written into the layout.
func safeSite(s Site) Site {
	if !branding.ValidHex(s.ThemeColor) {
		s.ThemeColor = branding.DefaultBrand
	}
	s.Look = branding.LookOrClean(s.Look)
	return s
}

// write sends a rendered body. A failed Write means the client has gone;
// there is nothing useful to do about it, and returning it would tell the
// caller to send an error page after the header has already gone out.
func write(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (r *Renderer) current() (*templateSet, error) {
	if r.reload {
		return r.parse()
	}
	return r.set, nil
}

func (r *Renderer) parse() (*templateSet, error) {
	layouts, err := filepath.Glob(filepath.Join(r.dir, "layouts", "*.html"))
	if err != nil {
		return nil, fmt.Errorf("tmpl: %w", err)
	}
	if !slices.ContainsFunc(layouts, func(f string) bool { return filepath.Base(f) == layoutName }) {
		return nil, fmt.Errorf("tmpl: no layouts/%s in %s", layoutName, r.dir)
	}
	set := &templateSet{
		pages:     map[string]*template.Template{},
		fragments: map[string]*template.Template{},
	}
	for _, name := range names(r.dir, "pages") {
		file := filepath.Join(r.dir, "pages", name+".html")
		t, err := template.New(name).Funcs(r.funcs).ParseFiles(append(slices.Clone(layouts), file)...)
		if err != nil {
			return nil, fmt.Errorf("tmpl: page %q: %w", name, err)
		}
		set.pages[name] = t
	}
	for _, name := range names(r.dir, "fragments") {
		file := filepath.Join(r.dir, "fragments", name+".html")
		t, err := template.New(name).Funcs(r.funcs).ParseFiles(file)
		if err != nil {
			return nil, fmt.Errorf("tmpl: fragment %q: %w", name, err)
		}
		set.fragments[name] = t
	}
	return set, nil
}

// names lists dir/sub/*.html without the extension, sorted.
func names(dir, sub string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, sub, "*.html")) // only fails on a bad pattern
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".html"))
	}
	slices.Sort(out)
	return out
}
