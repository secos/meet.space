// Package archtest holds structural tests: rules about the code itself that
// keep later phases honest (CLAUDE.md "The test setup").
package archtest

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"meet.space/internal/config"
	"meet.space/internal/testkit"
)

func root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("go.mod not found")
		}
		dir = next
	}
}

type goFile struct {
	rel  string // path relative to the repo root, with forward slashes
	file *ast.File
	fset *token.FileSet
	test bool
}

// goFiles parses every Go file under cmd/, internal/ and migrations/.
func goFiles(t *testing.T) []goFile {
	t.Helper()
	r := root(t)
	var out []goFile
	for _, top := range []string{"cmd", "internal", "migrations"} {
		err := filepath.WalkDir(filepath.Join(r, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
				return err
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(r, p)
			rel = filepath.ToSlash(rel)
			out = append(out, goFile{rel: rel, file: f, fset: fset, test: strings.HasSuffix(p, "_test.go")})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// importName is the name a file uses for an import path ("" if it doesn't
// import it).
func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

func under(rel string, dirs ...string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// P0-HARNESS-10: domain code takes time from the injected clock. No
// non-test code under internal/ outside the clock package and the test
// harness refers to time.Now, time.Since or time.Until, as a call or as a
// value, under any import name.
func TestNoWallClockOutsideClockPackage(t *testing.T) {
	for _, g := range goFiles(t) {
		if g.test || !under(g.rel, "internal") || under(g.rel, "internal/clock", "internal/testkit") {
			continue
		}
		name := importName(g.file, "time")
		if name == "" || name == "_" {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && slices.Contains([]string{"Now", "Since", "Until"}, sel.Sel.Name) {
				t.Errorf("%s: time.%s outside the clock package; take a clock.Clock", g.fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// P0-ROUTES-02: routes are registered only by the route table's builder.
// No other code calls Handle/HandleFunc on a mux or http.Handle/HandleFunc.
func TestRoutesAreRegisteredOnlyFromTheTable(t *testing.T) {
	for _, g := range goFiles(t) {
		if g.test || under(g.rel, "internal/route") {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc") {
				t.Errorf("%s: %s registers a route outside the route table", g.fset.Position(call.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// P0-HARNESS-08: every test package imports the network guard (through
// testkit, testkit/apptest or testkit/nonet).
func TestEveryTestPackageImportsTheNetworkGuard(t *testing.T) {
	guards := []string{"meet.space/internal/testkit", "meet.space/internal/testkit/apptest", "meet.space/internal/testkit/nonet"}
	pkgs := map[string]bool{} // dir → guarded
	for _, g := range goFiles(t) {
		if !g.test {
			continue
		}
		dir := filepath.Dir(g.rel)
		guarded := false
		for _, imp := range g.file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if slices.Contains(guards, p) {
				guarded = true
			}
		}
		pkgs[dir] = pkgs[dir] || guarded
	}
	for dir, ok := range pkgs {
		if !ok {
			t.Errorf("%s: test package doesn't import the network guard", dir)
		}
	}
}

// Critic addition: recording fakes and test helpers are never linked into
// the server binary.
func TestServerBinaryLinksNoTestDoubles(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./cmd/server")
	cmd.Dir = root(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "meet.space/internal/testkit") || strings.HasPrefix(line, "meet.space/internal/clock/clocktest") {
			t.Errorf("server links %s", line)
		}
	}
}

// P0-CONFIG-07: platform identity lives only in config. Go string
// literals, templates, our own static files, input.css and migrations never
// name the platform or its domains (decisions.md question 6).
func TestNoHardcodedPlatformIdentity(t *testing.T) {
	banned := regexp.MustCompile(`(?i)meet\.space|meetspace\.|yourcommunity|auth\.meet|mail\.meet`)
	for _, g := range goFiles(t) {
		if g.test || under(g.rel, "internal/testkit") {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ImportSpec:
				return false // the module path is meet.space
			case *ast.BasicLit:
				if n.Kind == token.STRING && banned.MatchString(n.Value) {
					t.Errorf("%s: platform identity in a string literal: %s", g.fset.Position(n.Pos()), n.Value)
				}
			}
			return true
		})
	}
	r := root(t)
	for _, dir := range []string{"templates", "static", "migrations"} {
		_ = filepath.WalkDir(filepath.Join(r, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(r, p)
			rel = filepath.ToSlash(rel)
			// Vendored third-party files and binary fonts are not ours.
			if strings.HasSuffix(rel, ".go") || strings.HasPrefix(rel, "static/fonts/") || rel == "static/js/htmx.min.js" ||
				rel == "static/js/htmx-LICENSE.txt" || rel == "static/css/output.css" {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if loc := banned.FindIndex(b); loc != nil {
				t.Errorf("%s: platform identity: %q", rel, b[loc[0]:loc[1]])
			}
			return nil
		})
	}
	if b, err := os.ReadFile(filepath.Join(r, "input.css")); err == nil && banned.Match(b) {
		t.Error("input.css names the platform")
	}
}

// P0-SECURITY-13 (Security Rule 9): HTML is rendered only through
// html/template, and no code turns data into trusted HTML, JS, CSS or URL
// values (the reviewed allowlist is empty in Phase 0).
func TestRule9_HTMLOnlyThroughHTMLTemplate(t *testing.T) {
	trusted := []string{"HTML", "HTMLAttr", "JS", "JSStr", "CSS", "URL", "Srcset"}
	for _, g := range goFiles(t) {
		if g.test {
			continue
		}
		if importName(g.file, "text/template") != "" {
			t.Errorf("%s imports text/template", g.rel)
		}
		name := importName(g.file, "html/template")
		if name == "" {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && slices.Contains(trusted, sel.Sel.Name) {
					t.Errorf("%s: converts data to template.%s", g.fset.Position(call.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
}

// envTags lists every environment variable the config struct reads.
func envTags() []string {
	var out []string
	rt := reflect.TypeOf(config.Config{})
	for i := range rt.NumField() {
		if tag, ok := rt.Field(i).Tag.Lookup("env"); ok {
			out = append(out, strings.Split(tag, ",")[0])
		}
	}
	return out
}

// composeEnv reads the app service's environment keys from a compose file
// (a small line-based reader; the file is ours and simple).
func composeEnv(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var keys []string
	inApp, inEnv := false, false
	key := regexp.MustCompile(`^      ([A-Z][A-Z0-9_]*):`)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "  app:"):
			inApp = true
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && inApp:
			inApp = false
		}
		if inApp && strings.HasPrefix(line, "    environment:") {
			inEnv = true
			continue
		}
		if inEnv && !strings.HasPrefix(line, "      ") {
			inEnv = false
		}
		if inApp && inEnv {
			if m := key.FindStringSubmatch(line); m != nil {
				keys = append(keys, m[1])
			}
		}
	}
	return keys
}

// P0-CONFIG-02: the dev compose file sets every variable the config reads
// (PLATFORM_NAME included) and nothing it doesn't.
func TestComposeDeclaresEveryConfigVariable(t *testing.T) {
	want := envTags()
	// These have working defaults and are left to them in development.
	optional := []string{"LISTEN_ADDR", "TEMPLATE_DIR", "STATIC_DIR", "CLIENT_IP_HEADER"}
	got := composeEnv(t, filepath.Join(root(t), "docker-compose.yml"))
	for _, k := range want {
		if !slices.Contains(got, k) && !slices.Contains(optional, k) {
			t.Errorf("docker-compose.yml doesn't set %s", k)
		}
	}
	for _, k := range got {
		if !slices.Contains(want, k) {
			t.Errorf("docker-compose.yml sets %s, which the config never reads", k)
		}
	}
	env, err := os.ReadFile(filepath.Join(root(t), ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range want {
		if !slices.Contains(optional, k) && !regexp.MustCompile(`(?m)^`+k+`=`).Match(env) {
			t.Errorf(".env.example doesn't list %s", k)
		}
	}
}

// P0-CONFIG-03: no static AWS credentials anywhere in config.
func TestNoStaticAWSCredentials(t *testing.T) {
	for _, k := range envTags() {
		if strings.HasPrefix(k, "AWS_") {
			t.Errorf("config reads %s", k)
		}
	}
	r := root(t)
	for _, f := range []string{"docker-compose.yml", "docker-compose.prod.yml", ".env.example", "Dockerfile"} {
		b, err := os.ReadFile(filepath.Join(r, f))
		if err != nil {
			t.Fatal(err)
		}
		if regexp.MustCompile(`AWS_(ACCESS_KEY_ID|SECRET_ACCESS_KEY|SESSION_TOKEN)`).Match(b) {
			t.Errorf("%s sets AWS credentials", f)
		}
	}
}

// P0-TOOLING-02 and the critic's Tailwind check: go.mod and the Dockerfile
// pin the same Go release; the Makefile and the Dockerfile pin the same
// Tailwind release.
func TestToolVersionsPinnedConsistently(t *testing.T) {
	r := root(t)
	gomod, _ := os.ReadFile(filepath.Join(r, "go.mod"))
	docker, _ := os.ReadFile(filepath.Join(r, "Dockerfile"))
	makefile, _ := os.ReadFile(filepath.Join(r, "Makefile"))
	goVer := regexp.MustCompile(`(?m)^go (\d+\.\d+\.\d+)$`).FindSubmatch(gomod)
	imgVer := regexp.MustCompile(`golang:(\d+\.\d+\.\d+)-alpine`).FindSubmatch(docker)
	if goVer == nil || imgVer == nil || string(goVer[1]) != string(imgVer[1]) {
		t.Errorf("go.mod go %s vs Dockerfile golang:%s", goVer, imgVer)
	}
	twMake := regexp.MustCompile(`TAILWIND_VERSION := (\S+)`).FindSubmatch(makefile)
	twDocker := regexp.MustCompile(`ARG TAILWIND_VERSION=(\S+)`).FindSubmatch(docker)
	if twMake == nil || twDocker == nil || string(twMake[1]) != string(twDocker[1]) {
		t.Errorf("Tailwind: Makefile %s vs Dockerfile %s", twMake, twDocker)
	}
}

// P0-TOOLING-09: the Makefile keeps the plan's targets and adds test and e2e.
func TestMakefileTargets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	phony := regexp.MustCompile(`(?m)^\.PHONY:(.*)$`).FindSubmatch(b)
	for _, target := range []string{"dev", "css", "migrate-up", "migrate-down", "build", "deploy", "test", "e2e"} {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).Match(b) {
			t.Errorf("Makefile has no %s target", target)
		}
		if phony == nil || !slices.Contains(strings.Fields(string(phony[1])), target) {
			t.Errorf("%s is not .PHONY", target)
		}
	}
	if !regexp.MustCompile(`go test -race -count=1 \./\.\.\.`).Match(b) || !regexp.MustCompile(`go vet \./\.\.\.`).Match(b) {
		t.Error("make test must run go vet ./... and go test -race -count=1 ./...")
	}
}

var _ = testkit.ErrNetworkInTest
