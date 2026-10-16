// Package designtokens_test checks input.css against docs/design.md and
// the built static/css/output.css against input.css. It has no non-test
// code: the tokens live in CSS.
package designtokens_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	_ "meet.space/internal/testkit" // network guard
)

const (
	inputCSS     = "../../input.css"
	outputCSS    = "../../static/css/output.css"
	templatesDir = "../../templates"
	scriptsDir   = "../../static/js"
	tailwindBin  = "../../bin/tailwindcss"
	buildHow     = "build it with `make css-build`, or by hand: ./bin/tailwindcss -i input.css -o static/css/output.css"
)

// rule is one CSS rule or at-rule block: its prelude ("@theme static",
// ":root", "@media (...)"), its declarations in order, and nested blocks.
type rule struct {
	prelude  string
	decls    []decl
	children []*rule
}

type decl struct{ prop, value string }

var commentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)

func stripComments(s string) string { return commentRE.ReplaceAllString(s, "") }

// parseCSS is a small parser, enough for our files: blocks, declarations
// and statement at-rules, with strings and parentheses respected.
func parseCSS(src string) *rule {
	src = stripComments(src)
	root := &rule{}
	i := 0
	var parse func(r *rule)
	parse = func(r *rule) {
		start := i
		depth := 0
		var quote byte
		for i < len(src) {
			c := src[i]
			switch {
			case quote != 0:
				if c == '\\' {
					i++
				} else if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'':
				quote = c
			case c == '(':
				depth++
			case c == ')':
				depth--
			case depth > 0:
			case c == '{':
				child := &rule{prelude: norm(src[start:i])}
				i++
				parse(child)
				r.children = append(r.children, child)
				start = i
				continue
			case c == ';':
				addDecl(r, src[start:i])
				start = i + 1
			case c == '}':
				addDecl(r, src[start:i])
				i++
				return
			}
			i++
		}
		addDecl(r, src[start:])
	}
	parse(root)
	return root
}

func addDecl(r *rule, s string) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "@") {
		return
	}
	prop, value, ok := strings.Cut(s, ":")
	if !ok {
		return
	}
	r.decls = append(r.decls, decl{strings.TrimSpace(prop), norm(value)})
}

// norm collapses white space so values compare across formatting.
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

// find returns every block, at any depth, whose prelude is p.
func (r *rule) find(p string) []*rule {
	var out []*rule
	for _, c := range r.children {
		if c.prelude == p {
			out = append(out, c)
		}
		out = append(out, c.find(p)...)
	}
	return out
}

// props returns the block's declarations as a map (last one wins).
func (r *rule) props() map[string]string {
	m := map[string]string{}
	for _, d := range r.decls {
		m[d.prop] = d.value
	}
	return m
}

// tokens returns the custom properties declared in the blocks, skipping
// namespace resets such as --color-*: initial.
func tokens(blocks ...*rule) map[string]string {
	m := map[string]string{}
	for _, b := range blocks {
		for _, d := range b.decls {
			if strings.HasPrefix(d.prop, "--") && !strings.HasSuffix(d.prop, "-*") {
				m[d.prop] = d.value
			}
		}
	}
	return m
}

const darkMedia = "@media (prefers-color-scheme: dark)"

// inputTokens reads input.css: the @theme static block (light values) and
// the :root block inside the dark media query.
func inputTokens(t *testing.T) (light, dark map[string]string, css *rule) {
	t.Helper()
	b, err := os.ReadFile(inputCSS)
	if err != nil {
		t.Fatal(err)
	}
	css = parseCSS(string(b))
	themes := css.find("@theme static")
	if len(themes) != 1 {
		t.Fatalf("input.css: want one @theme static block, found %d", len(themes))
	}
	return tokens(themes[0]), darkRoot(t, css, "input.css"), css
}

func darkRoot(t *testing.T, css *rule, file string) map[string]string {
	t.Helper()
	var roots []*rule
	for _, m := range css.find(darkMedia) {
		roots = append(roots, m.find(":root")...)
	}
	if len(roots) != 1 {
		t.Fatalf("%s: want one :root inside %s, found %d", file, darkMedia, len(roots))
	}
	return tokens(roots[0])
}

// builtCSS reads static/css/output.css, failing with how to build it when
// it is missing or stale. It is stale when it is older than one of
// Tailwind's sources (input.css, templates/, static/js/) and differs from
// what bin/tailwindcss builds from them now. Tailwind leaves the output
// file alone when its content would not change (v4.3.3), so an older
// file can still be current; the content comparison tells the two apart.
func builtCSS(t *testing.T) (string, *rule) {
	t.Helper()
	st, err := os.Stat(outputCSS)
	if err != nil {
		t.Fatalf("static/css/output.css is missing: %s", buildHow)
	}
	b, err := os.ReadFile(outputCSS)
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{inputCSS}
	for _, dir := range []string{templatesDir, scriptsDir} {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				sources = append(sources, p)
			}
			return nil
		})
	}
	for _, s := range sources {
		if sst, err := os.Stat(s); err == nil && sst.ModTime().After(st.ModTime()) {
			if fresh := buildFresh(t); !bytes.Equal(fresh, b) {
				t.Fatalf("static/css/output.css is out of date (older than %s, and a fresh build differs): %s",
					strings.TrimPrefix(s, "../../"), buildHow)
			}
			break
		}
	}
	return string(b), parseCSS(string(b))
}

// buildFresh runs bin/tailwindcss on input.css into a temporary file and
// returns the result.
func buildFresh(t *testing.T) []byte {
	t.Helper()
	bin, err := filepath.Abs(tailwindBin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("static/css/output.css is older than its sources and bin/tailwindcss is missing: %s", buildHow)
	}
	out := filepath.Join(t.TempDir(), "output.css")
	cmd := exec.Command(bin, "-i", "input.css", "-o", out)
	cmd.Dir = "../.."
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bin/tailwindcss: %v\n%s", err, msg)
	}
	fresh, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}

// outputTokens reads the built CSS: the theme layer's ":root, :host"
// block (light) and the :root block inside the dark media query.
func outputTokens(t *testing.T) (light, dark map[string]string, css *rule) {
	t.Helper()
	_, css = builtCSS(t)
	var roots []*rule
	for _, layer := range css.find("@layer theme") {
		roots = append(roots, layer.find(":root, :host")...)
	}
	if len(roots) != 1 {
		t.Fatalf("output.css: want one ':root, :host' block in @layer theme, found %d", len(roots))
	}
	return tokens(roots[0]), darkRoot(t, css, "output.css"), css
}

// Self-check for the parser: nested blocks, strings with braces, and
// declarations after a nested block.
func TestParseCSS(t *testing.T) {
	css := parseCSS(`@import "x" source(none);
/* { not a block } */
@theme static { --a: #fff; --b: url("/x{y}.woff2") format("woff2"); --c-*: initial; }
@layer theme { :root { color-scheme: light; } @media (prefers-color-scheme: dark) { :root { --a: #000; } } }
body { color: var(--a) }`)
	th := css.find("@theme static")
	if len(th) != 1 {
		t.Fatalf("theme blocks: %d", len(th))
	}
	if got := tokens(th[0]); len(got) != 2 || got["--a"] != "#fff" || got["--b"] != `url("/x{y}.woff2") format("woff2")` {
		t.Errorf("theme tokens = %v", got)
	}
	if got := darkRoot(t, css, "sample"); got["--a"] != "#000" {
		t.Errorf("dark = %v", got)
	}
	if got := css.find("body"); len(got) != 1 || got[0].props()["color"] != "var(--a)" {
		t.Errorf("body = %v", got)
	}
}
