package designtokens_test

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Expected values below are copied from docs/design.md, not from
// input.css, so a change on either side fails here.

var varRE = regexp.MustCompile(`^var\((--[A-Za-z0-9-]+)\)$`)

// modes returns every token's value in light and in dark mode, with
// var(--x) references resolved. A token with no dark value keeps its
// light one in dark mode.
func modes(t *testing.T) (light, dark map[string]string) {
	t.Helper()
	l, d, _ := inputTokens(t)
	light = map[string]string{}
	dark = map[string]string{}
	for k, v := range l {
		light[k], dark[k] = v, v
	}
	for k, v := range d {
		dark[k] = v
	}
	resolve := func(m map[string]string) {
		for k := range m {
			v := m[k]
			for range 10 {
				ref := varRE.FindStringSubmatch(v)
				if ref == nil {
					break
				}
				v = m[ref[1]]
			}
			m[k] = v
		}
	}
	resolve(light)
	resolve(dark)
	return light, dark
}

func checkExact(t *testing.T, mode string, got map[string]string, want map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if g, ok := got[k]; !ok {
			t.Errorf("%s: %s is missing, want %s", mode, k, want[k])
		} else if !strings.EqualFold(g, want[k]) {
			t.Errorf("%s: %s = %s, design.md says %s", mode, k, g, want[k])
		}
	}
}

// P0-LAYOUT-01
// The Clean neutrals, light values (design.md 2.1).
func TestNeutralTokens_Light(t *testing.T) {
	light, _, _ := inputTokens(t)
	checkExact(t, "light", light, map[string]string{
		"--color-ground":  "#f5f7fa",
		"--color-surface": "#ffffff",
		"--color-ink":     "#172033",
		"--color-muted":   "#5d6779",
		"--color-line":    "#e3e8ef",
	})
}

// P0-LAYOUT-02
// The Clean neutrals, dark values (design.md 2.1), under
// prefers-color-scheme: dark, with color-scheme set so native controls
// follow (design.md 2.2).
func TestNeutralTokens_DarkAndColorScheme(t *testing.T) {
	_, dark, css := inputTokens(t)
	checkExact(t, "dark", dark, map[string]string{
		"--color-ground":  "#0f141c",
		"--color-surface": "#171e2a",
		"--color-ink":     "#e8edf5",
		"--color-muted":   "#98a3b6",
		"--color-line":    "#263041",
	})
	var lightScheme []string
	for _, layer := range css.find("@layer theme") {
		for _, c := range layer.children {
			if c.prelude == ":root" {
				lightScheme = append(lightScheme, c.props()["color-scheme"])
			}
		}
	}
	if !slices.Equal(lightScheme, []string{"light"}) {
		t.Errorf("light :root color-scheme = %q, want [light]", lightScheme)
	}
	for _, m := range css.find(darkMedia) {
		for _, r := range m.find(":root") {
			if got := r.props()["color-scheme"]; got != "dark" {
				t.Errorf("dark :root color-scheme = %q, want dark", got)
			}
		}
	}
}

// P0-LAYOUT-03
// Semantic colors, light and dark (design.md 2.1). Info is the brand's
// text color on tint-1 in both modes ("same"), so it has no dark value of
// its own and follows brand-text and tint-1.
func TestSemanticTokens(t *testing.T) {
	light, dark, _ := inputTokens(t)
	checkExact(t, "light", light, map[string]string{
		"--color-success":    "#166534",
		"--color-success-bg": "#dcfce7",
		"--color-warning":    "#92400e",
		"--color-warning-bg": "#fef3c7",
		"--color-danger":     "#b91c1c",
		"--color-danger-bg":  "#fee2e2",
		"--color-info":       "var(--color-brand-text)",
		"--color-info-bg":    "var(--color-tint-1)",
	})
	checkExact(t, "dark", dark, map[string]string{
		"--color-success":    "#4ade80",
		"--color-success-bg": "#123222",
		"--color-warning":    "#fbbf24",
		"--color-warning-bg": "#3a2a08",
		"--color-danger":     "#f87171",
		"--color-danger-bg":  "#3b1414",
	})
	for _, k := range []string{"--color-info", "--color-info-bg"} {
		if v, ok := dark[k]; ok {
			t.Errorf("dark: %s = %s; info is the same in both modes", k, v)
		}
	}
}

// P0-LAYOUT-04
// Brand-colored text (design.md 4.3): light mode lowers the brand's OKLCH
// lightness and dark mode raises it, hue unchanged, until it reaches
// 4.5:1 against surface. The token is exactly the first color that
// procedure reaches (the brand itself when it already passes), and it
// also reaches 4.5:1 on ground, where links sit too (design.md 2.1).
func TestBrandText_ContrastAndHue(t *testing.T) {
	light, dark := modes(t)
	brand := toOKLCH(t, light["--color-brand"])
	for _, m := range []struct {
		name string
		v    map[string]string
	}{{"light", light}, {"dark", dark}} {
		bt := m.v["--color-brand-text"]
		if want := brandTextFor(t, light["--color-brand"], m.v["--color-surface"], m.name == "dark"); !strings.EqualFold(bt, want) {
			t.Errorf("%s: brand-text = %s; design.md 4.3 applied to %s on surface %s gives %s",
				m.name, bt, light["--color-brand"], m.v["--color-surface"], want)
		}
		for _, bg := range []string{"--color-surface", "--color-ground"} {
			if c := contrast(t, bt, m.v[bg]); c < 4.5 {
				t.Errorf("%s: brand-text %s on %s %s is %.2f:1, want >= 4.5", m.name, bt, bg, m.v[bg], c)
			}
		}
		got := toOKLCH(t, bt)
		// 8-bit rounding moves the hue of a saturated blue by well under
		// half a degree.
		if d := hueDiff(got.H, brand.H); d > 0.5 {
			t.Errorf("%s: brand-text %s hue %.2f, brand hue %.2f", m.name, bt, got.H, brand.H)
		}
		switch m.name {
		case "light":
			if got.L > brand.L+0.002 {
				t.Errorf("light: brand-text L %.4f is above the brand's %.4f; 4.3 lowers it", got.L, brand.L)
			}
		case "dark":
			if got.L < brand.L-0.002 {
				t.Errorf("dark: brand-text L %.4f is below the brand's %.4f; 4.3 raises it", got.L, brand.L)
			}
		}
	}
}

// P0-LAYOUT-05
// The brand is the platform default #2563eb; text on it is white because
// white reaches 4.5:1 on it (design.md 4.2). Neither changes in dark mode
// (design.md 2.2).
func TestBrandTokens(t *testing.T) {
	light, dark, _ := inputTokens(t)
	checkExact(t, "light", light, map[string]string{
		"--color-brand":          "#2563eb",
		"--color-brand-contrast": "#ffffff",
	})
	if c := contrast(t, "#ffffff", light["--color-brand"]); c < 4.5 {
		t.Errorf("white on the brand is %.2f:1; 4.2 would not pick white", c)
	}
	for _, k := range []string{"--color-brand", "--color-brand-contrast"} {
		if v, ok := dark[k]; ok {
			t.Errorf("dark: %s = %s; it does not change in dark mode", k, v)
		}
	}
}

// P0-LAYOUT-06
// Accents and tints (design.md 4.1) for the default brand: valid hex;
// tints have dark values and accents don't; and each one is exactly the
// table's OKLCH color, with chroma reduced only as far as needed to fit
// sRGB, rounded to 8-bit hex.
//
// The hex is compared exactly; the OKLCH check after it explains a
// mismatch. Its tolerances cover 8-bit rounding: lightness 0.005, chroma
// 0.005, hue 1.5 degrees (a low-chroma tint's hue moves most).
func TestDerivedAccentsAndTints(t *testing.T) {
	light, dark, _ := inputTokens(t)
	brand := toOKLCH(t, light["--color-brand"])
	type target struct{ L, C, dh float64 }
	spec := []struct {
		name  string
		light target
		dark  *target
	}{
		{"accent-1", target{0.76, 0.14, 150}, nil},
		{"accent-2", target{0.82, 0.14, 60}, nil},
		{"accent-3", target{0.74, 0.15, 270}, nil},
		{"tint-1", target{0.95, 0.04, 0}, &target{0.30, 0.06, 0}},
		{"tint-2", target{0.95, 0.045, 150}, &target{0.30, 0.05, 150}},
		{"tint-3", target{0.96, 0.05, 60}, &target{0.30, 0.05, 60}},
		{"tint-4", target{0.95, 0.045, 270}, &target{0.30, 0.05, 270}},
	}
	check := func(mode, name, hex string, want target) {
		t.Helper()
		if !hexRE.MatchString(hex) {
			t.Errorf("%s: --color-%s = %q, not a #rrggbb hex", mode, name, hex)
			return
		}
		h := math.Mod(brand.H+want.dh, 360)
		wantC := fittedChroma(want.L, want.C, h)
		if wantHex := fromOKLCH(want.L, wantC, h); !strings.EqualFold(hex, wantHex) {
			t.Errorf("%s: --color-%s = %s, design.md 4.1 gives %s", mode, name, hex, wantHex)
		}
		got := toOKLCH(t, hex)
		if math.Abs(got.L-want.L) > 0.005 || math.Abs(got.C-wantC) > 0.005 || hueDiff(got.H, h) > 1.5 {
			t.Errorf("%s: --color-%s %s is oklch(%.4f %.4f %.2f), want oklch(%.2f %.4f %.2f)",
				mode, name, hex, got.L, got.C, got.H, want.L, wantC, h)
		}
	}
	for _, s := range spec {
		k := "--color-" + s.name
		check("light", s.name, light[k], s.light)
		v, hasDark := dark[k]
		switch {
		case s.dark == nil && hasDark:
			t.Errorf("dark: %s = %s; accents are the same in both modes", k, v)
		case s.dark != nil && !hasDark:
			t.Errorf("dark: %s has no dark value", k)
		case s.dark != nil:
			check("dark", s.name, v, *s.dark)
		}
	}
}

// P0-LAYOUT-07
// Text contrast (design.md 2.1, 9): ink and muted on ground and surface,
// each semantic text color on its own background and on ground and
// surface, in both modes, and text on the brand, all at least 4.5:1.
//
// OPEN (Phase 1 design conflict, not asserted): the pill pair, brand-text
// on tint-1 (design.md 3 "Pill / badge", and Info's own background), is
// 4.47:1 in light and 3.69:1 in dark with the 4.1 tints.
// OPEN (Phase 1 design conflict, not asserted): --color-line is 1.15-1.39:1
// against ground and surface, while 4.4 and 9 ask 3:1 for UI borders.
func TestContrastTable(t *testing.T) {
	light, dark := modes(t)
	for _, m := range []struct {
		name string
		v    map[string]string
	}{{"light", light}, {"dark", dark}} {
		pair := func(fg, bg string) {
			t.Helper()
			f, b := m.v[fg], m.v[bg]
			if c := contrast(t, f, b); c < 4.5 {
				t.Errorf("%s: %s %s on %s %s is %.2f:1, want >= 4.5", m.name, fg, f, bg, b, c)
			}
		}
		for _, fg := range []string{"--color-ink", "--color-muted", "--color-info"} {
			pair(fg, "--color-ground")
			pair(fg, "--color-surface")
		}
		for _, s := range []string{"success", "warning", "danger"} {
			fg := "--color-" + s
			pair(fg, fg+"-bg")
			pair(fg, "--color-ground")
			pair(fg, "--color-surface")
		}
		pair("--color-brand-contrast", "--color-brand")
	}
}

// px converts "12px", "0.75rem" or "1rem" to pixels (1rem = 16px).
func px(t *testing.T, v string) float64 {
	t.Helper()
	for unit, scale := range map[string]float64{"px": 1, "rem": 16} {
		if n, ok := strings.CutSuffix(v, unit); ok {
			f, err := strconv.ParseFloat(n, 64)
			if err == nil {
				return f * scale
			}
		}
	}
	t.Fatalf("%q is not a px or rem length", v)
	return 0
}

// P0-LAYOUT-08
// The type scale (design.md 2.3), exactly, and nothing else; Figtree with
// the fallback stack.
func TestTypeScale(t *testing.T) {
	light, _, _ := inputTokens(t)
	scale := []struct {
		step       string
		size, line float64
	}{
		{"xs", 12, 16}, {"sm", 14, 20}, {"base", 16, 24}, {"lg", 18, 26},
		{"xl", 20, 26}, {"2xl", 26, 30}, {"3xl", 32, 36},
	}
	var want []string
	for _, s := range scale {
		k := "--text-" + s.step
		want = append(want, k, k+"--line-height")
		if got := px(t, light[k]); got != s.size {
			t.Errorf("%s = %s (%vpx), want %vpx", k, light[k], got, s.size)
		}
		if got := px(t, light[k+"--line-height"]); got != s.line {
			t.Errorf("%s--line-height = %s (%vpx), want %vpx", k, light[k+"--line-height"], got, s.line)
		}
	}
	for k := range light {
		if strings.HasPrefix(k, "--text-") && !slices.Contains(want, k) {
			t.Errorf("%s is not a step in design.md 2.3", k)
		}
	}
	if got := light["--font-sans"]; got != `"Figtree", ui-sans-serif, system-ui, sans-serif` {
		t.Errorf("--font-sans = %s", got)
	}
}

// leadingZero writes ".06" as "0.06" so design.md's shorthand compares.
var leadingZeroRE = regexp.MustCompile(`(^|[^0-9])\.([0-9])`)

func leadingZero(s string) string { return leadingZeroRE.ReplaceAllString(s, "${1}0.$2") }

// P0-LAYOUT-09
// Space, shape and elevation (design.md 2.4): 4px spacing base, radius
// sm 8 / md 10 / lg 14, the content column of 56rem, breakpoints 640 and
// 1024 only, and the card shadow in light with none in dark.
func TestSpaceShapeElevation(t *testing.T) {
	light, dark, _ := inputTokens(t)
	for k, want := range map[string]float64{
		"--spacing":        4,
		"--radius-sm":      8,
		"--radius-md":      10,
		"--radius-lg":      14,
		"--container-page": 896,
		"--breakpoint-sm":  640,
		"--breakpoint-lg":  1024,
	} {
		if v, ok := light[k]; !ok {
			t.Errorf("%s is missing", k)
		} else if got := px(t, v); got != want {
			t.Errorf("%s = %s (%vpx), want %vpx", k, v, got, want)
		}
	}
	for k := range light {
		if strings.HasPrefix(k, "--radius-") && !slices.Contains([]string{"--radius-sm", "--radius-md", "--radius-lg"}, k) {
			t.Errorf("%s is not in design.md 2.4", k)
		}
		if strings.HasPrefix(k, "--breakpoint-") && k != "--breakpoint-sm" && k != "--breakpoint-lg" {
			t.Errorf("%s is not in design.md 2.4", k)
		}
	}
	const card = "0 1px 2px rgb(23 32 51 / .06), 0 8px 24px rgb(23 32 51 / .06)"
	if got := leadingZero(light["--shadow-card"]); got != leadingZero(card) {
		t.Errorf("--shadow-card = %s, want %s", light["--shadow-card"], card)
	}
	if got := dark["--shadow-card"]; got != "none" {
		t.Errorf("dark --shadow-card = %q, want none", got)
	}
}

// P0-LAYOUT-14
// The built CSS has every token from input.css as a custom property with
// the same value: the light ones on :root (even tokens no template uses,
// so a group's style block and component CSS can rely on them) and the
// dark ones in the prefers-color-scheme: dark block.
func TestBuiltCSS_EmitsEveryToken(t *testing.T) {
	inLight, inDark, _ := inputTokens(t)
	outLight, outDark, _ := outputTokens(t)
	compare := func(mode string, in, out map[string]string) {
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			got, ok := out[k]
			switch {
			case !ok:
				t.Errorf("%s: output.css has no %s", mode, k)
			case leadingZero(got) != leadingZero(in[k]):
				t.Errorf("%s: output.css %s = %s, input.css says %s", mode, k, got, in[k])
			}
		}
	}
	compare("light", inLight, outLight)
	compare("dark", inDark, outDark)
	if len(inLight) < 40 {
		t.Errorf("only %d light tokens parsed from input.css; the parser missed some", len(inLight))
	}
	if len(outDark) != len(inDark) {
		t.Errorf("output.css dark block has %d tokens, input.css %d", len(outDark), len(inDark))
	}

	// The dark :root and the light ":root, :host" have the same
	// specificity, so the dark values apply only if the dark block comes
	// later in the same cascade layer, or is unlayered.
	_, css := builtCSS(t)
	lightAt, darkAt := -1, -1
	for i, c := range css.children {
		if c.prelude == "@layer theme" && len(c.find(":root, :host")) > 0 && lightAt < 0 {
			lightAt = i
		}
		if (c.prelude == "@layer theme" && len(c.find(darkMedia)) > 0) || c.prelude == darkMedia {
			darkAt = i
		}
	}
	switch {
	case lightAt < 0 || darkAt < 0:
		t.Errorf("output.css: light theme block at %d, dark block at %d; want both in @layer theme or the dark one unlayered", lightAt, darkAt)
	case css.children[darkAt].prelude == "@layer theme" && darkAt < lightAt:
		t.Errorf("output.css: the dark block comes before the light theme block in @layer theme, so the light values win in dark mode")
	}
}
