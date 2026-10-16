package designtokens_test

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// design.md 2.3 (body text, headings), 5 rule 4 (long unbroken strings
// wrap), 3 and 9 (3px brand focus ring, 2px offset). No P0 id was given
// for this test.
// The built CSS styles the body and headings and shows a focus ring on
// every interactive element.
func TestBaseStyles_TypeAndFocus(t *testing.T) {
	_, css := builtCSS(t)
	body := mergedProps(css, "body")
	for k, want := range map[string]string{
		"background-color": "var(--color-ground)",
		"color":            "var(--color-ink)",
		"font-family":      "var(--font-sans)",
		"font-size":        "var(--text-base)",
		"line-height":      "var(--text-base--line-height)",
		"overflow-wrap":    "anywhere",
	} {
		if body[k] != want {
			t.Errorf("body %s = %q, want %q", k, body[k], want)
		}
	}
	h := mergedProps(css, "h1, h2, h3, h4, h5, h6")
	for k, want := range map[string]string{
		"font-weight":    "700",
		"letter-spacing": "-0.015em",
		"text-wrap":      "balance",
	} {
		if h[k] != want {
			t.Errorf("headings %s = %q, want %q", k, h[k], want)
		}
	}
	focus := mergedProps(css, ":focus-visible")
	if focus["outline"] != "3px solid var(--color-brand)" || focus["outline-offset"] != "2px" {
		t.Errorf(":focus-visible = %v, want a 3px brand outline offset 2px", focus)
	}
}

// design.md 2.5 and 9 (everything animated stops under reduced motion;
// 200ms fade on htmx swaps). No P0 id was given for this test.
// Under prefers-reduced-motion: reduce every element and pseudo-element
// has animation and transition switched off with !important, from the
// base layer (so later layers' !important can't win); swapped-in htmx
// content fades in over 200ms.
func TestBaseStyles_MotionAndHtmxFade(t *testing.T) {
	_, css := builtCSS(t)
	var reduced []*rule
	for _, layer := range css.find("@layer base") {
		for _, m := range layer.find("@media (prefers-reduced-motion: reduce)") {
			reduced = append(reduced, m.find("*, *::before, *::after")...)
		}
	}
	if len(reduced) != 1 {
		t.Fatalf("want one reduced-motion rule for *, *::before, *::after in @layer base, found %d", len(reduced))
	}
	p := reduced[0].props()
	if p["animation"] != "none !important" || p["transition"] != "none !important" {
		t.Errorf("reduced motion = %v, want animation and transition none !important", p)
	}

	fade := mergedProps(css, "[data-htmx-powered] > *, .htmx-added")
	if fade["transition"] != "opacity 200ms ease-out" {
		t.Errorf("htmx swap transition = %q, want opacity 200ms", fade["transition"])
	}
	var start []*rule
	for _, s := range css.find("@starting-style") {
		start = append(start, s.find("[data-htmx-powered] > *, .htmx-added")...)
	}
	if len(start) != 1 || start[0].props()["opacity"] != "0" {
		t.Errorf("want @starting-style opacity 0 for swapped-in htmx content")
	}
}

// development-plan.md Phase 0 step 5; the brief for this slice (scan
// templates and our scripts only, never docs/). No P0 id was given for
// this test.
// input.css turns off Tailwind's automatic source detection and names
// templates/ and static/js/ as its only sources.
func TestInputCSS_ScansOnlyTemplatesAndScripts(t *testing.T) {
	src := stripComments(readInput(t))
	if !regexp.MustCompile(`@import\s+"tailwindcss"\s+source\(none\)\s*;`).MatchString(src) {
		t.Error(`input.css: want @import "tailwindcss" source(none);`)
	}
	var sources []string
	for _, m := range regexp.MustCompile(`@source\s+(not\s+)?"([^"]+)"`).FindAllStringSubmatch(src, -1) {
		sources = append(sources, m[1]+m[2])
	}
	slices.Sort(sources)
	if !slices.Equal(sources, []string{"./static/js", "./templates"}) {
		t.Errorf("@source = %q, want ./static/js and ./templates", sources)
	}
}

// mergedProps merges the declarations of every block, at any depth, whose
// prelude is p.
func mergedProps(css *rule, p string) map[string]string {
	m := map[string]string{}
	for _, r := range css.find(p) {
		for k, v := range r.props() {
			m[k] = v
		}
	}
	return m
}

func readInput(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(inputCSS)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
