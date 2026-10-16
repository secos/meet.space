package tmpl_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"

	_ "meet.space/internal/testkit" // network guard
	"meet.space/internal/tmpl"
)

// templatesDir is the real template directory, from internal/tmpl.
const templatesDir = "../../templates"

// platformName is a platform name for tests: the string that must never
// appear on a group's site (white-label, design.md 1.6).
const platformName = "meet.space"

var platformSite = tmpl.Site{
	Kind:       "platform",
	SiteName:   platformName,
	FooterText: platformName,
	ThemeColor: "#2563eb",
	Look:       "clean",
}

var groupSite = tmpl.Site{
	Kind:       "group",
	SiteName:   "Prairie Hikers",
	FooterText: "Prairie Hikers",
	ThemeColor: "#7c3aed",
	Look:       "lively",
	Initials:   "PH",
}

// fakeAssets returns a fixed fingerprint and records the names asked for.
type fakeAssets struct {
	mu    sync.Mutex
	names []string
}

func (f *fakeAssets) URL(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
	return "/static/" + name + "?v=0123456789"
}

func newRenderer(t *testing.T) *tmpl.Renderer {
	t.Helper()
	r, err := tmpl.New(templatesDir, &fakeAssets{}, false)
	if err != nil {
		t.Fatalf("tmpl.New: %v", err)
	}
	return r
}

// contentFor gives each page the Content its handler will pass.
func contentFor(page string) any {
	switch page {
	case "error":
		return tmpl.ErrorContent{Status: 404, Message: "We can't find that page.", LinkText: "Go to the home page", LinkHref: "/"}
	}
	return nil
}

func renderPage(t *testing.T, r *tmpl.Renderer, page string, p tmpl.Page) (*httptest.ResponseRecorder, *html.Node) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := r.Render(rec, http.StatusOK, page, p); err != nil {
		t.Fatalf("Render(%q): %v", page, err)
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse %q: %v", page, err)
	}
	return rec, doc
}

func findAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func byTag(tag string) func(*html.Node) bool {
	return func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == tag }
}

func elements(doc *html.Node, tag string) []*html.Node { return findAll(doc, byTag(tag)) }

func one(t *testing.T, doc *html.Node, tag string) *html.Node {
	t.Helper()
	els := elements(doc, tag)
	if len(els) != 1 {
		t.Fatalf("want exactly one <%s>, found %d", tag, len(els))
	}
	return els[0]
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// text is the node's text content with runs of white space collapsed.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func metaNamed(doc *html.Node, name string) []*html.Node {
	return findAll(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "meta" {
			return false
		}
		v, _ := attr(n, "name")
		return v == name
	})
}

// writeFiles creates files (slash paths relative to dir) with contents.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// spyWriter records every use of the ResponseWriter.
type spyWriter struct {
	header      http.Header
	wroteHeader bool
	body        []byte
}

func newSpy() *spyWriter { return &spyWriter{header: http.Header{}} }

func (s *spyWriter) Header() http.Header { return s.header }
func (s *spyWriter) WriteHeader(int)     { s.wroteHeader = true }
func (s *spyWriter) Write(b []byte) (int, error) {
	s.body = append(s.body, b...)
	return len(b), nil
}

func (s *spyWriter) untouched() bool {
	return !s.wroteHeader && len(s.body) == 0 && len(s.header) == 0
}
