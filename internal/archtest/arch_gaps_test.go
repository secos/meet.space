package archtest

import (
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/caarlos0/env/v11"

	"meet.space/internal/config"
)

// P0-HARNESS-10: a dot import of "time" turns time.Now() into a bare Now()
// that TestNoWallClockOutsideClockPackage can't see. Domain code never
// dot-imports time.
func TestNoWallClockOutsideClockPackage_NoDotImport(t *testing.T) {
	for _, g := range goFiles(t) {
		if g.test || !under(g.rel, "internal") || under(g.rel, "internal/clock", "internal/testkit") {
			continue
		}
		if importName(g.file, "time") == "." {
			t.Errorf("%s dot-imports time; take a clock.Clock", g.rel)
		}
	}
}

// P0-HARNESS-10: clock.Real is the wall clock. Domain code that calls
// clock.Real{}.Now() has the same bug as time.Now(); only cmd/server wires
// it (internal/clock's own doc comment).
func TestRealClockWiredOnlyInCmdServer(t *testing.T) {
	for _, g := range goFiles(t) {
		if g.test || under(g.rel, "cmd/server", "internal/clock") {
			continue
		}
		name := importName(g.file, "meet.space/internal/clock")
		if name == "" || name == "_" {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && sel.Sel.Name == "Real" {
				t.Errorf("%s: clock.Real outside cmd/server; take a clock.Clock", g.fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

// P0-HARNESS-08: every test binary in the module links the network guard,
// wherever its package lives (TestEveryTestPackageImportsTheNetworkGuard
// only walks cmd/, internal/ and migrations/).
func TestEveryTestBinaryLinksTheNetworkGuard(t *testing.T) {
	cmd := exec.Command("go", "list", "-test", "-f", `{{.ImportPath}}{{"\t"}}{{join .Deps " "}}`, "./...")
	cmd.Dir = root(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path, deps, _ := strings.Cut(line, "\t")
		if !strings.HasSuffix(path, ".test") {
			continue
		}
		n++
		if !slices.Contains(strings.Fields(deps), "meet.space/internal/testkit/nonet") {
			t.Errorf("%s doesn't link the network guard (import testkit, testkit/apptest or testkit/nonet)", path)
		}
	}
	if n == 0 {
		t.Fatal("go list found no test binaries")
	}
}

// P0-CONFIG-02: envTags (which the compose and AWS checks rely on) lists
// exactly the variables the env library will read, nested structs
// included.
func TestEnvTagsMatchWhatTheConfigReads(t *testing.T) {
	params, err := env.GetFieldParams(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, p := range params {
		if !p.Ignored && p.Key != "" {
			read = append(read, p.Key)
		}
	}
	slices.Sort(read)
	tags := envTags()
	for _, k := range read {
		if !slices.Contains(tags, k) {
			t.Errorf("config reads %s, which envTags (and so the compose and AWS checks) can't see", k)
		}
	}
	for _, k := range tags {
		if !slices.Contains(read, k) {
			t.Errorf("envTags lists %s, which the config never reads", k)
		}
	}
}

// P0-CONFIG-02: the config is the only reader of the process environment
// (config.FromEnviron: only cmd/server passes it). Code that calls
// os.Getenv reads a variable the compose check can't see.
func TestOnlyCmdServerReadsTheProcessEnvironment(t *testing.T) {
	banned := map[string][]string{
		"os":      {"Getenv", "LookupEnv", "Environ", "ExpandEnv"},
		"syscall": {"Getenv", "Environ"},
	}
	for _, g := range goFiles(t) {
		if g.test || under(g.rel, "cmd/server", "internal/testkit") {
			continue
		}
		for pkg, funcs := range banned {
			name := importName(g.file, pkg)
			if name == "" || name == "_" {
				continue
			}
			ast.Inspect(g.file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && slices.Contains(funcs, sel.Sel.Name) {
					t.Errorf("%s: %s.%s outside cmd/server; add a config field", g.fset.Position(sel.Pos()), pkg, sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// P0-SECURITY-13 (Security Rule 9): HTML is written only by html/template.
// No Go string literal holds an HTML tag (a handler that builds HTML with
// fmt or string concatenation skips escaping), except the reviewed
// allowlist: the bare 404, which has no data in it (decisions.md "Error
// pages").
func TestRule9_NoHandWrittenHTML(t *testing.T) {
	allowed := map[string][]string{"internal/route/route.go": {"BareNotFoundBody"}}
	tag := regexp.MustCompile(`(?i)<(!doctype\b|/?[a-z][a-z0-9-]*(\s[^<>]*)?/?>)`)
	for _, g := range goFiles(t) {
		if g.test || under(g.rel, "internal/testkit") {
			continue
		}
		ast.Inspect(g.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ImportSpec:
				return false
			case *ast.ValueSpec:
				for _, id := range n.Names {
					if slices.Contains(allowed[g.rel], id.Name) {
						return false
					}
				}
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(n.Value)
				if err != nil {
					s = n.Value
				}
				if m := tag.FindString(s); m != "" {
					t.Errorf("%s: HTML %q in a Go string; render it through html/template", g.fset.Position(n.Pos()), m)
				}
			}
			return true
		})
	}
}

// P0-TOOLING-09: the test target itself runs go vet and the race-enabled,
// uncached suite (TestMakefileTargets only checks those commands appear
// somewhere in the Makefile).
func TestMakeTestRunsVetAndRaceSuite(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	target := regexp.MustCompile(`^test\s*:`)
	var recipe []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case target.MatchString(line):
			in = true
		case in && strings.HasPrefix(line, "\t"):
			recipe = append(recipe, strings.TrimSpace(line))
		case in:
			in = false
		}
	}
	for _, want := range []string{"go vet ./...", "go test -race -count=1 ./..."} {
		if !slices.Contains(recipe, want) {
			t.Errorf("make test doesn't run %q (recipe: %q)", want, recipe)
		}
	}
}
