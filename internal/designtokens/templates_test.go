package designtokens_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const palette = `slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|mauve|olive|mist|taupe`

// namedColors are the CSS Color 4 named colors.
const namedColors = `aliceblue|antiquewhite|aqua|aquamarine|azure|beige|bisque|black|blanchedalmond|blue|blueviolet|brown|burlywood|cadetblue|chartreuse|chocolate|coral|cornflowerblue|cornsilk|crimson|cyan|darkblue|darkcyan|darkgoldenrod|darkgray|darkgreen|darkgrey|darkkhaki|darkmagenta|darkolivegreen|darkorange|darkorchid|darkred|darksalmon|darkseagreen|darkslateblue|darkslategray|darkslategrey|darkturquoise|darkviolet|deeppink|deepskyblue|dimgray|dimgrey|dodgerblue|firebrick|floralwhite|forestgreen|fuchsia|gainsboro|ghostwhite|gold|goldenrod|gray|green|greenyellow|grey|honeydew|hotpink|indianred|indigo|ivory|khaki|lavender|lavenderblush|lawngreen|lemonchiffon|lightblue|lightcoral|lightcyan|lightgoldenrodyellow|lightgray|lightgreen|lightgrey|lightpink|lightsalmon|lightseagreen|lightskyblue|lightslategray|lightslategrey|lightsteelblue|lightyellow|lime|limegreen|linen|magenta|maroon|mediumaquamarine|mediumblue|mediumorchid|mediumpurple|mediumseagreen|mediumslateblue|mediumspringgreen|mediumturquoise|mediumvioletred|midnightblue|mintcream|mistyrose|moccasin|navajowhite|navy|oldlace|olive|olivedrab|orange|orangered|orchid|palegoldenrod|palegreen|paleturquoise|palevioletred|papayawhip|peachpuff|peru|pink|plum|powderblue|purple|rebeccapurple|red|rosybrown|royalblue|saddlebrown|salmon|sandybrown|seagreen|seashell|sienna|silver|skyblue|slateblue|slategray|slategrey|snow|springgreen|steelblue|tan|teal|thistle|tomato|turquoise|violet|wheat|white|whitesmoke|yellow|yellowgreen`

// hasLiteralColor reports whether a CSS value has a hex color, a color
// function or a named color in it.
func hasLiteralColor(v string) bool {
	return hexLiteralRE.MatchString(v) || colorFuncRE.MatchString(v) || namedColorRE.MatchString(v)
}

var (
	// A Tailwind default-palette color utility, with any variants, e.g.
	// bg-gray-50, dark:text-red-700, hover:border-blue-500/50, bg-white.
	defaultPaletteRE = regexp.MustCompile(`(?:^|:)[a-z-]+-(?:(?:` + palette + `)-(?:50|[1-9]00|950)|white|black)(?:/\S+)?$`)
	// A color literal: #rgb, #rrggbb (also #rgba, #rrggbbaa), or a color
	// function.
	hexLiteralRE = regexp.MustCompile(`#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})\b`)
	colorFuncRE  = regexp.MustCompile(`(?i)\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(`)
	// A CSS named color (CSS Color 4) as a whole word, e.g. the red in
	// bg-[red] or "outline: 3px solid blue". Hyphens and underscores count
	// as part of a word, so token names like --color-ink and classes like
	// text-wrap never match.
	namedColorRE  = regexp.MustCompile(`(?i)(?:^|[^\w-])(` + namedColors + `)(?:$|[^\w-])`)
	arbitraryRE   = regexp.MustCompile(`\[([^\]]*)\]`)
	classAttrRE   = regexp.MustCompile(`(?s)\bclass="([^"]*)"`)
	styleAttrRE   = regexp.MustCompile(`(?s)\bstyle="([^"]*)"`)
	styleElemRE   = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	templActionRE = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
)

// colorProblems lists the default-palette utilities and color literals in
// a template's markup (class and style attributes, <style> elements, and
// color functions anywhere).
func colorProblems(src string) []string {
	var out []string
	for _, m := range classAttrRE.FindAllStringSubmatch(src, -1) {
		for _, class := range strings.Fields(templActionRE.ReplaceAllString(m[1], " ")) {
			if defaultPaletteRE.MatchString(class) {
				out = append(out, "default palette class "+class)
			}
			if hexLiteralRE.MatchString(class) {
				out = append(out, "color literal in class "+class)
			}
			for _, v := range arbitraryRE.FindAllStringSubmatch(class, -1) {
				if namedColorRE.MatchString(strings.ReplaceAll(v[1], "_", " ")) {
					out = append(out, "named color in class "+class)
				}
			}
		}
	}
	var css []string
	for _, m := range styleAttrRE.FindAllStringSubmatch(src, -1) {
		css = append(css, m[1])
	}
	for _, m := range styleElemRE.FindAllStringSubmatch(src, -1) {
		css = append(css, m[1])
	}
	for _, c := range css {
		for _, lit := range hexLiteralRE.FindAllString(c, -1) {
			out = append(out, "color literal "+lit)
		}
		for _, m := range namedColorRE.FindAllStringSubmatch(c, -1) {
			out = append(out, "named color "+m[1])
		}
	}
	for _, f := range colorFuncRE.FindAllString(src, -1) {
		out = append(out, "color function "+f)
	}
	return out
}

// Self-check: what colorProblems flags and what it lets through.
func TestColorProblems(t *testing.T) {
	for _, bad := range []string{
		`<p class="bg-gray-50">`, `<p class="text-red-700">`, `<p class="dark:bg-slate-900">`,
		`<p class="hover:border-blue-500/50">`, `<p class="bg-white">`, `<p class="text-black">`,
		`<p class="bg-[#fff]">`, `<p class="x {{if .A}}bg-zinc-100{{end}}">`,
		`<p style="color:#123456">`, `<style>a{color:#abc}</style>`, `<p style="color: rgb(0 0 0)">`,
		`<p class="text-[oklch(0.5_0.1_200)]">`, `<p class="bg-[red]">`, `<p class="text-[color:tomato]">`,
		`<p class="ring-[gold]">`, `<p class="shadow-[0_0_2px_black]">`, `<p style="outline: 1px solid navy">`,
	} {
		if len(colorProblems(bad)) == 0 {
			t.Errorf("not flagged: %s", bad)
		}
	}
	for _, good := range []string{
		`<p class="bg-surface text-ink border-line">`, `<p class="text-brand-text bg-tint-1">`,
		`<a href="#feed" hx-target="#add">`, `<p class="text-danger bg-danger-bg">`,
		`<p class="rounded-sm size-8 text-2xl sm:text-3xl">`, `<p class="{{.Class}}">`,
		`<p class="text-wrap grid-cols-[1fr_auto] bg-(--color-tint-1) text-[13px]">`,
	} {
		if p := colorProblems(good); len(p) != 0 {
			t.Errorf("flagged %s: %v", good, p)
		}
	}
}

// P0-LAYOUT-13
// Templates use tokens only (design.md 2.2): no Tailwind default-palette
// color utilities (gray-*, white, black, red-* ...) and no hex or color
// function literals. In input.css, color literals appear only as token
// definitions (custom properties).
func TestTemplatesAndCSS_UseTokensOnly(t *testing.T) {
	n := 0
	err := filepath.WalkDir(templatesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		n++
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, problem := range colorProblems(string(b)) {
			t.Errorf("%s: %s", strings.TrimPrefix(p, "../../"), problem)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no templates found")
	}

	_, _, css := inputTokens(t)
	var walk func(r *rule)
	walk = func(r *rule) {
		for _, d := range r.decls {
			if strings.HasPrefix(d.prop, "--") {
				continue
			}
			if hasLiteralColor(d.value) {
				t.Errorf("input.css %s { %s: %s }: color literal outside a token definition", r.prelude, d.prop, d.value)
			}
		}
		for _, c := range r.children {
			walk(c)
		}
	}
	walk(css)

	// What ships: no rule in the built CSS has a literal color, outside
	//   - the theme layer, where the tokens are defined (checked against
	//     input.css by P0-LAYOUT-14), and
	//   - Tailwind's registrations of its own --tw-* variables (@property
	//     initial values and their @layer properties fallback, such as
	//     --tw-shadow: 0 0 #0000), which every shadow or ring utility adds.
	// Fully transparent #0000 (shadow-none) is not a visible color. This
	// also catches arbitrary-value utilities generated from static/js.
	_, built := builtCSS(t)
	transparentHex := regexp.MustCompile(`#0000(?:0000)?\b`)
	var walkBuilt func(r *rule)
	walkBuilt = func(r *rule) {
		if r.prelude == "@layer theme" || r.prelude == "@layer properties" || strings.HasPrefix(r.prelude, "@property ") {
			return
		}
		for _, d := range r.decls {
			if hasLiteralColor(transparentHex.ReplaceAllString(d.value, "transparent")) {
				t.Errorf("output.css %s { %s: %s }: color literal outside a token definition", r.prelude, d.prop, d.value)
			}
		}
		for _, c := range r.children {
			walkBuilt(c)
		}
	}
	walkBuilt(built)
}
