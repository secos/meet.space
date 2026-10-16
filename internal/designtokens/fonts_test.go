package designtokens_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	fontSrcRE  = regexp.MustCompile(`^url\("(/static/fonts/([^"]+))"\) format\("woff2"\)$`)
	fontHashRE = regexp.MustCompile(`\.([0-9a-f]{8})\.woff2$`)
	urlRE      = regexp.MustCompile(`url\(\s*["']?([^"')]*)`)
	// Hosts that serve web fonts. A white-label page must never contact one
	// (design.md 2.3, development-plan.md Phase 0 step 6).
	fontHosts = []string{"fonts.googleapis.com", "fonts.gstatic.com", "fonts.bunny.net", "use.typekit.net", "fast.fonts.net", "use.fontawesome.com"}
)

// P0-LAYOUT-11
// The built CSS loads Figtree (normal and italic) from our own host: each
// @font-face uses font-display: swap, a root-relative /static/fonts/ URL
// whose file exists and carries the first 8 hex of its own sha256, and a
// weight range covering 400-800 (design.md 2.3). Nothing in the CSS or
// the templates points at a font host.
func TestFontFace_BuiltCSS(t *testing.T) {
	raw, css := builtCSS(t)
	faces := css.find("@font-face")
	styles := map[string]bool{}
	for _, f := range faces {
		p := f.props()
		if p["font-family"] != `"Figtree"` {
			t.Errorf("@font-face font-family = %s", p["font-family"])
		}
		styles[p["font-style"]] = true
		if p["font-display"] != "swap" {
			t.Errorf("@font-face %s: font-display = %q, want swap", p["font-style"], p["font-display"])
		}
		w := strings.Fields(p["font-weight"])
		if len(w) == 0 {
			w = []string{"missing"}
		}
		lo, err1 := strconv.Atoi(w[0])
		hi, err2 := strconv.Atoi(w[len(w)-1])
		if err1 != nil || err2 != nil || lo > 400 || hi < 800 {
			t.Errorf("@font-face %s: font-weight %q does not cover 400-800", p["font-style"], p["font-weight"])
		}
		m := fontSrcRE.FindStringSubmatch(p["src"])
		if m == nil {
			t.Errorf("@font-face %s: src %q, want one root-relative /static/fonts/ woff2 URL", p["font-style"], p["src"])
			continue
		}
		body, err := os.ReadFile(filepath.Join("../../static/fonts", m[2]))
		if err != nil {
			t.Errorf("@font-face src %s: %v", m[1], err)
			continue
		}
		sum := sha256.Sum256(body)
		h := fontHashRE.FindStringSubmatch(m[2])
		if h == nil || h[1] != hex.EncodeToString(sum[:])[:8] {
			t.Errorf("@font-face src %s: name does not carry the file's sha256 prefix %s", m[1], hex.EncodeToString(sum[:])[:8])
		}
	}
	if !styles["normal"] || !styles["italic"] || len(faces) != 2 {
		t.Errorf("want Figtree normal and italic faces, got %d faces: %v", len(faces), styles)
	}

	code := stripComments(raw)
	for _, u := range urlRE.FindAllStringSubmatch(code, -1) {
		if !strings.HasPrefix(u[1], "/") || strings.HasPrefix(u[1], "//") {
			t.Errorf("output.css url(%s) is not a root-relative path on our own host", u[1])
		}
	}
	for _, host := range fontHosts {
		if strings.Contains(code, host) {
			t.Errorf("output.css mentions font host %s", host)
		}
	}
	if strings.Contains(code, "http://") || strings.Contains(code, "https://") {
		t.Errorf("output.css has an absolute URL outside comments")
	}

	externalLink := regexp.MustCompile(`(?i)<link[^>]+href="(https?:)?//`)
	_ = filepath.WalkDir(templatesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if externalLink.Match(b) {
			t.Errorf("%s loads a stylesheet or font from another host", p)
		}
		for _, host := range fontHosts {
			if strings.Contains(string(b), host) {
				t.Errorf("%s mentions font host %s", p, host)
			}
		}
		return nil
	})
}
