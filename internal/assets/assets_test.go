package assets_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"meet.space/internal/assets"
	_ "meet.space/internal/testkit" // network guard
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}

func newAssets(t *testing.T, dir string, dev bool) *assets.Assets {
	t.Helper()
	a, err := assets.New(dir, dev)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	return a
}

func get(t *testing.T, a *assets.Assets, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// captureWarnings collects slog warnings for the rest of the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// Phase 0 step 6 ("fingerprinted"); route GET /static/* ("Fingerprinted
// assets, cached for a year"). No P0 id was given for this test.
// URL is /static/<name>?v=<first 10 hex of the content's sha256>.
func TestURL_IsStaticPathWithContentHash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/output.css", "body{}")
	writeFile(t, dir, "js/htmx.min.js", "var htmx")
	for _, dev := range []bool{false, true} {
		a := newAssets(t, dir, dev)
		if got, want := a.URL("css/output.css"), "/static/css/output.css?v="+hash("body{}", 10); got != want {
			t.Errorf("dev=%v: URL = %q, want %q", dev, got, want)
		}
		if got, want := a.URL("js/htmx.min.js"), "/static/js/htmx.min.js?v="+hash("var htmx", 10); got != want {
			t.Errorf("dev=%v: URL = %q, want %q", dev, got, want)
		}
	}
}

// Phase 0 step 6 (development: a rebuilt stylesheet gets a new URL). No P0
// id was given for this test.
// In dev mode the fingerprint follows the file's content when its size or
// modification time changes.
func TestURL_DevModeFingerprintChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "css", "output.css")
	writeFile(t, dir, "css/output.css", "aaaa")
	a := newAssets(t, dir, true)
	first := a.URL("css/output.css")

	// Same size, new modification time.
	writeFile(t, dir, "css/output.css", "bbbb")
	if err := os.Chtimes(p, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	second := a.URL("css/output.css")
	if second == first || second != "/static/css/output.css?v="+hash("bbbb", 10) {
		t.Errorf("after same-size edit: %q (before %q)", second, first)
	}

	// New size, modification time put back to the previous one.
	writeFile(t, dir, "css/output.css", "ccccc")
	if err := os.Chtimes(p, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if third := a.URL("css/output.css"); third != "/static/css/output.css?v="+hash("ccccc", 10) {
		t.Errorf("after size change: %q", third)
	}

	// A file created after New is found too.
	writeFile(t, dir, "js/new.js", "x")
	if got := a.URL("js/new.js"); got != "/static/js/new.js?v="+hash("x", 10) {
		t.Errorf("new file: %q", got)
	}
}

// Phase 0 step 6. No P0 id was given for this test.
// A name that is not a servable file gets the plain URL and a logged
// warning, in both modes.
func TestURL_UnknownNameIsUnversionedAndWarns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/a.css", "a")
	writeFile(t, dir, ".env", "SECRET=1")
	writeFile(t, filepath.Dir(dir), "outside.txt", "x")
	for _, dev := range []bool{false, true} {
		a := newAssets(t, dir, dev)
		for _, name := range []string{"missing.css", "css", ".env", "../outside.txt", "css/../css/a.css"} {
			logs := captureWarnings(t)
			if got := a.URL(name); got != "/static/"+name {
				t.Errorf("dev=%v: URL(%q) = %q, want it unversioned", dev, name, got)
			}
			if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), name) {
				t.Errorf("dev=%v: URL(%q) logged %q, want a warning naming it", dev, name, logs.String())
			}
		}
	}
}

// Phase 0 step 6 (go test -race). No P0 id was given for this test.
// URL is safe to call from many requests at once in dev mode.
func TestURL_ConcurrentUseInDevMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/a.css", "a")
	a := newAssets(t, dir, true)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 50 {
				_ = a.URL("css/a.css")
			}
		})
	}
	wg.Wait()
}

// Route GET /static/* (development-plan.md "Other"); design.md 2.3 (fonts
// served from the visitor's own hostname). No P0 id was given for this test.
// Files are served with a Content-Type chosen by extension, and no
// Cache-Control or X-Content-Type-Options (the route and middleware layers
// own those).
func TestHandler_ServesFilesWithContentTypeByExtension(t *testing.T) {
	dir := t.TempDir()
	want := map[string]string{
		"css/output.css":               "text/css; charset=utf-8",
		"js/htmx.min.js":               "text/javascript; charset=utf-8",
		"fonts/figtree.6b96ce6d.woff2": "font/woff2",
		"fonts/Figtree-OFL.txt":        "text/plain; charset=utf-8",
		"img/icon.svg":                 "image/svg+xml",
		"img/icon.png":                 "image/png",
		"img/other.bin":                "application/octet-stream",
		"page.html":                    "application/octet-stream",
	}
	for name := range want {
		writeFile(t, dir, name, "content of "+name)
	}
	a := newAssets(t, dir, false)
	for name, ct := range want {
		rec := get(t, a, http.MethodGet, "/static/"+name)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", name, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); got != ct {
			t.Errorf("%s: Content-Type %q, want %q", name, got, ct)
		}
		if got := rec.Body.String(); got != "content of "+name {
			t.Errorf("%s: body %q", name, got)
		}
		for _, h := range []string{"Cache-Control", "X-Content-Type-Options"} {
			if v, ok := rec.Header()[h]; ok {
				t.Errorf("%s: %s set to %q", name, h, v)
			}
		}
	}
}

// Route GET /static/*. No P0 id was given for this test.
// HEAD gets the headers with no body.
func TestHandler_HEAD(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/a.css", "body{color:red}")
	a := newAssets(t, dir, false)
	rec := get(t, a, http.MethodHead, "/static/css/a.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "15" {
		t.Errorf("Content-Length %q, want 15", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type %q", got)
	}
}

// Route GET /static/* (the ?v= only changes the URL). No P0 id was given
// for this test.
// The query string is ignored: any ?v= gets the current file.
func TestHandler_IgnoresQueryString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/a.css", "now")
	a := newAssets(t, dir, false)
	for _, target := range []string{"/static/css/a.css?v=0000000000", "/static/css/a.css?v=", "/static/css/a.css?x=../../etc/passwd"} {
		rec := get(t, a, http.MethodGet, target)
		if rec.Code != http.StatusOK || rec.Body.String() != "now" {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
	}
}

// Route GET /static/*; Security Rule 13's spirit (nothing private reaches
// visitors). No P0 id was given for this test.
// Nothing outside the directory is reachable: ../ (plain or encoded,
// checked after decoding), symlinks that point out, and absolute paths all
// get 404 and never the file.
func TestHandler_TraversalNeverEscapes(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "static")
	writeFile(t, dir, "css/a.css", "a")
	writeFile(t, base, "outside/secret.txt", "TOP SECRET")
	if err := os.Symlink(filepath.Join(base, "outside", "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(dir, "linkdir")); err != nil {
		t.Fatal(err)
	}
	// Relative, from static/css/: up two levels reaches base/outside.
	if err := os.Symlink("../../outside/secret.txt", filepath.Join(dir, "css", "rel.txt")); err != nil {
		t.Fatal(err)
	}
	// Each symlink really does reach the secret, so a 404 below is the
	// handler refusing it, not a dangling link.
	for _, l := range []string{"link.txt", "linkdir/secret.txt", "css/rel.txt"} {
		if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(l))); err != nil || string(b) != "TOP SECRET" {
			t.Fatalf("test setup: %s does not reach the secret: %q %v", l, b, err)
		}
	}
	a := newAssets(t, dir, false)
	for _, target := range []string{
		"/static/../outside/secret.txt",
		"/static/css/../../outside/secret.txt",
		"/static/%2e%2e/outside/secret.txt",
		"/static/%2E%2E/outside/secret.txt",
		"/static/css/%2e%2e/%2e%2e/outside/secret.txt",
		"/static/%2e%2e%2foutside%2fsecret.txt",
		"/static/..%2foutside%2fsecret.txt",
		"/static/link.txt",
		"/static/linkdir/secret.txt",
		"/static/css/rel.txt",
		"/static//outside/secret.txt",
		"/static/" + filepath.ToSlash(filepath.Join(base, "outside", "secret.txt")),
	} {
		rec := get(t, a, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "TOP SECRET") {
			t.Errorf("%s: served the file outside the directory", target)
		}
	}
	// An escaping symlink is not fingerprinted either.
	if got := a.URL("link.txt"); strings.Contains(got, "?v=") {
		t.Errorf("URL of an escaping symlink = %q", got)
	}
}

// Route GET /static/*. No P0 id was given for this test.
// Directories get 404, the /static/ root included.
func TestHandler_DirectoriesAre404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/a.css", "a")
	a := newAssets(t, dir, false)
	for _, target := range []string{"/static/", "/static", "/static/css", "/static/css/"} {
		if rec := get(t, a, http.MethodGet, target); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rec.Code)
		}
	}
}

// Route GET /static/*. No P0 id was given for this test.
// Dotfiles, dot directories and any segment starting with "." get 404,
// as do missing files and paths outside /static/.
func TestHandler_DotfilesAndMissingFilesAre404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "css/a.css", "a")
	writeFile(t, dir, ".env", "SECRET=1")
	writeFile(t, dir, ".git/config", "[core]")
	writeFile(t, dir, "css/.hidden.css", "h")
	a := newAssets(t, dir, false)
	for _, target := range []string{
		"/static/.env",
		"/static/.git/config",
		"/static/css/.hidden.css",
		"/static/./css/a.css",
		"/static/css/./a.css",
		"/static/missing.css",
		"/css/a.css",
		"/other/static/css/a.css",
	} {
		rec := get(t, a, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rec.Code)
		}
		if v, ok := rec.Header()["Cache-Control"]; ok {
			t.Errorf("%s: 404 sets Cache-Control %q", target, v)
		}
	}
}

var hashedFont = regexp.MustCompile(`\.([0-9a-f]{8})\.woff2$`)

// isLicence reports whether a file under static/fonts is a font licence.
func isLicence(name string) bool {
	u := strings.ToUpper(name)
	return strings.HasSuffix(u, ".TXT") && (strings.Contains(u, "OFL") || strings.Contains(u, "LICENSE"))
}

// Phase 0 step 6 (font files fingerprinted); design.md 2.3. No P0 id was
// given for this test.
// Every file under static/fonts except the licence carries the first 8 hex
// digits of its own sha256 in its name.
func TestStaticFonts_NamesCarryTheirOwnHash(t *testing.T) {
	const fonts = "../../static/fonts"
	entries, err := os.ReadDir(fonts)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || isLicence(e.Name()) {
			continue
		}
		n++
		body, err := os.ReadFile(filepath.Join(fonts, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m := hashedFont.FindStringSubmatch(e.Name())
		if m == nil {
			t.Errorf("%s: name has no .<hash8>.woff2", e.Name())
			continue
		}
		if want := hash(string(body), 8); m[1] != want {
			t.Errorf("%s: hash in name %s, content hash %s (rename the file)", e.Name(), m[1], want)
		}
	}
	if n == 0 {
		t.Error("no font files under static/fonts")
	}
}

// requireBuiltCSS returns static/css/output.css, failing with how to build
// it if it is missing or stale: older than one of Tailwind's sources
// (input.css, templates/, static/js/) and different from what
// bin/tailwindcss builds now. Tailwind leaves the output file alone when
// its content would not change (v4.3.3), so an older file can be current.
func requireBuiltCSS(t *testing.T) string {
	t.Helper()
	const out = "../../static/css/output.css"
	const how = "build it with `make css-build`, or by hand: ./bin/tailwindcss -i input.css -o static/css/output.css"
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("static/css/output.css is missing: %s", how)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{"../../input.css"}
	for _, dir := range []string{"../../templates", "../../static/js"} {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				sources = append(sources, p)
			}
			return nil
		})
	}
	for _, s := range sources {
		sst, err := os.Stat(s)
		if err != nil || !sst.ModTime().After(st.ModTime()) {
			continue
		}
		bin, err := filepath.Abs("../../bin/tailwindcss")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("static/css/output.css is older than %s and bin/tailwindcss is missing: %s", strings.TrimPrefix(s, "../../"), how)
		}
		fresh := filepath.Join(t.TempDir(), "output.css")
		cmd := exec.Command(bin, "-i", "input.css", "-o", fresh)
		cmd.Dir = "../.."
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bin/tailwindcss: %v\n%s", err, msg)
		}
		if b, err := os.ReadFile(fresh); err != nil || !bytes.Equal(b, body) {
			t.Fatalf("static/css/output.css is out of date (older than %s, and a fresh build differs): %s", strings.TrimPrefix(s, "../../"), how)
		}
		break
	}
	return string(body)
}

// P0-LAYOUT-11 (font URLs in the built CSS point at the fingerprinted files)
// output.css refers to every font file by its fingerprinted name, and
// every font URL in it exists under static/fonts.
func TestOutputCSS_ReferencesFingerprintedFontNames(t *testing.T) {
	css := requireBuiltCSS(t)
	refs := regexp.MustCompile(`url\(\s*["']?/static/fonts/([^"')]+)["']?\s*\)`).FindAllStringSubmatch(css, -1)
	if len(refs) == 0 {
		t.Fatal("output.css has no /static/fonts/ URLs")
	}
	referenced := map[string]bool{}
	for _, m := range refs {
		referenced[m[1]] = true
		if !hashedFont.MatchString(m[1]) {
			t.Errorf("output.css refers to %s, which is not a fingerprinted name", m[1])
		}
		if _, err := os.Stat(filepath.Join("../../static/fonts", m[1])); err != nil {
			t.Errorf("output.css refers to %s, which does not exist", m[1])
		}
	}
	entries, _ := os.ReadDir("../../static/fonts")
	for _, e := range entries {
		if !e.IsDir() && !isLicence(e.Name()) && !referenced[e.Name()] {
			t.Errorf("static/fonts/%s is not referenced by output.css", e.Name())
		}
	}
}

// Route GET /static/* with the real static/ directory. No P0 id was given
// for this test.
// The real files the layout loads are served with their types.
func TestHandler_RealStaticFiles(t *testing.T) {
	requireBuiltCSS(t)
	a := newAssets(t, "../../static", false)
	for target, ct := range map[string]string{
		"/static/css/output.css":                               "text/css; charset=utf-8",
		"/static/js/htmx.min.js":                               "text/javascript; charset=utf-8",
		"/static/fonts/figtree-variable.6b96ce6d.woff2":        "font/woff2",
		"/static/fonts/figtree-italic-variable.3662c283.woff2": "font/woff2",
	} {
		rec := get(t, a, http.MethodGet, target)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ct || rec.Body.Len() == 0 {
			t.Errorf("%s: %d %q (%d bytes)", target, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
}
