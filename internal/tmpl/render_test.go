package tmpl_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"meet.space/internal/assets"
	"meet.space/internal/tmpl"
)

// Phase 0 step 6 and 11 (renderer, base layout, placeholder home page,
// error pages). No P0 id was given for this test.
// Every page parses with the layout and executes, and every fragment
// parses alone and executes, for a platform site and a group site.
func TestEveryPageAndFragmentRendersForPlatformAndGroup(t *testing.T) {
	r := newRenderer(t)
	for _, want := range []string{"home_platform", "home_group", "error"} {
		if !slices.Contains(r.Pages(), want) {
			t.Errorf("Pages() = %q, missing %q", r.Pages(), want)
		}
	}
	if !slices.Contains(r.Fragments(), "error") {
		t.Errorf("Fragments() = %q, missing error", r.Fragments())
	}
	for _, site := range []tmpl.Site{platformSite, groupSite} {
		for _, page := range r.Pages() {
			rec := httptest.NewRecorder()
			if err := r.Render(rec, http.StatusOK, page, tmpl.Page{Site: site, Content: contentFor(page)}); err != nil {
				t.Errorf("%s site, page %q: %v", site.Kind, page, err)
				continue
			}
			if !strings.HasPrefix(rec.Body.String(), "<!DOCTYPE html>") {
				t.Errorf("%s site, page %q: not a full page:\n%s", site.Kind, page, rec.Body)
			}
		}
		for _, frag := range r.Fragments() {
			rec := httptest.NewRecorder()
			if err := r.RenderFragment(rec, http.StatusOK, frag, tmpl.ErrorContent{Message: "x"}); err != nil {
				t.Errorf("fragment %q: %v", frag, err)
			}
		}
	}
}

// Phase 0 step 11, development-plan.md "Base Layout"; design.md 2.3
// (16px inputs so iOS does not zoom: zoom is never disabled), 9.
// No P0 id was given for this test.
// The layout's fixed structure.
func TestLayout_Structure(t *testing.T) {
	r := newRenderer(t)
	for _, site := range []tmpl.Site{platformSite, groupSite} {
		t.Run(site.Kind, func(t *testing.T) {
			rec, doc := renderPage(t, r, "home_"+site.Kind, tmpl.Page{Site: site})
			body := rec.Body.String()

			if !strings.HasPrefix(body, "<!DOCTYPE html>") {
				t.Errorf("does not start with <!DOCTYPE html>: %.40q", body)
			}
			if lang, _ := attr(one(t, doc, "html"), "lang"); lang != "en" {
				t.Errorf(`<html lang> = %q, want "en"`, lang)
			}
			charset := findAll(doc, func(n *html.Node) bool {
				v, ok := attr(n, "charset")
				return n.Data == "meta" && ok && strings.EqualFold(v, "utf-8")
			})
			if len(charset) != 1 {
				t.Errorf("want one <meta charset=UTF-8>, found %d", len(charset))
			}

			vp := metaNamed(doc, "viewport")
			if len(vp) != 1 {
				t.Fatalf("want one viewport meta, found %d", len(vp))
			}
			content, _ := attr(vp[0], "content")
			if !strings.Contains(content, "width=device-width") || !strings.Contains(content, "initial-scale=1") {
				t.Errorf("viewport = %q", content)
			}
			for _, banned := range []string{"maximum-scale", "user-scalable", "minimum-scale"} {
				if strings.Contains(content, banned) {
					t.Errorf("viewport %q restricts zoom (%s)", content, banned)
				}
			}

			if got := text(one(t, doc, "title")); got != site.SiteName {
				t.Errorf("<title> = %q, want the site name %q", got, site.SiteName)
			}

			tc := metaNamed(doc, "theme-color")
			if len(tc) != 1 {
				t.Fatalf("want one theme-color meta, found %d", len(tc))
			}
			if v, _ := attr(tc[0], "content"); v != site.ThemeColor {
				t.Errorf("theme-color = %q, want %q", v, site.ThemeColor)
			}

			sheets := findAll(doc, func(n *html.Node) bool {
				rel, _ := attr(n, "rel")
				return n.Data == "link" && rel == "stylesheet"
			})
			if len(sheets) != 1 {
				t.Fatalf("want one stylesheet, found %d", len(sheets))
			}
			if href, _ := attr(sheets[0], "href"); href != "/static/css/output.css?v=0123456789" {
				t.Errorf("stylesheet href = %q, want the asset URL of css/output.css", href)
			}
			scripts := elements(doc, "script")
			if len(scripts) != 1 {
				t.Fatalf("want one <script> (htmx), found %d", len(scripts))
			}
			if src, _ := attr(scripts[0], "src"); src != "/static/js/htmx.min.js?v=0123456789" {
				t.Errorf("script src = %q, want the asset URL of js/htmx.min.js", src)
			}

			for _, tag := range []string{"nav", "main", "footer"} {
				one(t, doc, tag)
			}
			if look, _ := attr(one(t, doc, "body"), "data-look"); look != site.Look {
				t.Errorf("data-look = %q, want %q", look, site.Look)
			}

			nav := one(t, doc, "nav")
			home := findAll(nav, func(n *html.Node) bool {
				href, _ := attr(n, "href")
				return n.Data == "a" && href == "/"
			})
			if len(home) != 1 || !strings.Contains(text(home[0]), site.SiteName) {
				t.Errorf("nav: want one link to / with the site name")
			}
			actions := findAll(nav, func(n *html.Node) bool {
				id, _ := attr(n, "id")
				return id == "nav-actions"
			})
			if len(actions) != 1 || actions[0].FirstChild != nil {
				t.Errorf("nav: want one empty #nav-actions (filled in Phase 1/3)")
			}
			if got := text(one(t, doc, "footer")); got != site.FooterText {
				t.Errorf("footer = %q, want %q", got, site.FooterText)
			}
		})
	}
}

// Phase 0 step 6 ("fingerprinted, served from the visitor's own
// hostname"). No P0 id was given for this test.
// With the real static/ directory, the layout's stylesheet and htmx URLs
// are /static/ URLs carrying the content hash.
func TestLayout_StylesheetAndHtmxAreFingerprintedStaticURLs(t *testing.T) {
	if _, err := os.Stat("../../static/css/output.css"); err != nil {
		t.Fatalf("static/css/output.css is missing: build it with `make css-build` (or: "+
			"./bin/tailwindcss -i input.css -o static/css/output.css): %v", err)
	}
	a, err := assets.New("../../static", false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := tmpl.New(templatesDir, a, false)
	if err != nil {
		t.Fatal(err)
	}
	_, doc := renderPage(t, r, "home_platform", tmpl.Page{Site: platformSite})
	sheet := findAll(doc, func(n *html.Node) bool {
		rel, _ := attr(n, "rel")
		return n.Data == "link" && rel == "stylesheet"
	})
	href, _ := attr(sheet[0], "href")
	if !regexp.MustCompile(`^/static/css/output\.css\?v=[0-9a-f]{10}$`).MatchString(href) {
		t.Errorf("stylesheet href = %q", href)
	}
	src, _ := attr(one(t, doc, "script"), "src")
	if !regexp.MustCompile(`^/static/js/htmx\.min\.js\?v=[0-9a-f]{10}$`).MatchString(src) {
		t.Errorf("htmx src = %q", src)
	}
}

// P0-SECURITY-14
// Rule 9: user content is escaped by html/template, never rendered raw. A
// group name full of markup shows as text in the title, the nav and the
// footer, and adds no element to the page.
func TestRule9_GroupNameIsEscaped(t *testing.T) {
	const evil = `<script>alert(1)</script> & "quotes" <img src=x onerror=alert(2)>`
	site := groupSite
	site.SiteName = evil
	site.FooterText = evil
	site.Initials = `<b>`
	r := newRenderer(t)
	for _, page := range r.Pages() {
		t.Run(page, func(t *testing.T) {
			rec, doc := renderPage(t, r, page, tmpl.Page{Site: site, Content: contentFor(page)})
			body := rec.Body.String()
			for _, raw := range []string{"<script>alert", "<img", `"quotes"`, "<b>"} {
				if strings.Contains(body, raw) {
					t.Errorf("raw %q reached the page", raw)
				}
			}
			if n := len(elements(doc, "script")); n != 1 {
				t.Errorf("%d <script> elements, want only htmx's", n)
			}
			if n := len(elements(doc, "img")) + len(elements(doc, "b")); n != 0 {
				t.Errorf("the name added %d elements", n)
			}
			if got := text(one(t, doc, "title")); got != evil {
				t.Errorf("<title> text = %q, want the name as text", got)
			}
			if got := text(one(t, doc, "nav")); !strings.Contains(got, evil) {
				t.Errorf("nav text = %q, want the name as text", got)
			}
			if got := text(one(t, doc, "footer")); got != evil {
				t.Errorf("footer text = %q, want the name as text", got)
			}
		})
	}
}

// P0-SECURITY-14 (Rule 9 applied to the error page and fragment; no
// separate id was given)
// The error page's words come from the caller, and the "Try again" link is
// the current URL (decisions.md "Error pages"), which the request
// controls. Markup in any of them shows as text, and a javascript: URL
// never becomes the link's href.
func TestRule9_ErrorTextAndLinkAreEscaped(t *testing.T) {
	const evil = `<script>alert(1)</script> & "quotes" <img src=x onerror=alert(2)>`
	r := newRenderer(t)
	for _, href := range []string{`javascript:alert(3)`, ` JavaScript:alert(3)`, `/x"><script>alert(4)</script>`} {
		c := tmpl.ErrorContent{Status: 500, Message: evil, LinkText: evil, LinkHref: href}
		for _, site := range []tmpl.Site{platformSite, groupSite} {
			rec, doc := renderPage(t, r, "error", tmpl.Page{Site: site, Content: c})
			body := rec.Body.String()
			for _, raw := range []string{"<script>alert", "<img", `"quotes"`, `"><script>`} {
				if strings.Contains(body, raw) {
					t.Errorf("%s site, href %q: raw %q reached the page", site.Kind, href, raw)
				}
			}
			if n := len(elements(doc, "script")); n != 1 {
				t.Errorf("%s site, href %q: %d <script> elements, want only htmx's", site.Kind, href, n)
			}
			links := elements(one(t, doc, "main"), "a")
			if len(links) != 1 {
				t.Fatalf("%s site, href %q: %d links in main, want one", site.Kind, href, len(links))
			}
			got, _ := attr(links[0], "href")
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(got)), "javascript:") {
				t.Errorf("%s site: href %q reached the page as %q", site.Kind, href, got)
			}
			if text(links[0]) != evil {
				t.Errorf("%s site: link text %q, want the words as text", site.Kind, text(links[0]))
			}
		}
	}
	rec := httptest.NewRecorder()
	if err := r.RenderFragment(rec, http.StatusInternalServerError, "error", tmpl.ErrorContent{Message: evil}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"<script>alert", "<img", `"quotes"`} {
		if strings.Contains(rec.Body.String(), raw) {
			t.Errorf("fragment: raw %q reached the response", raw)
		}
	}
}

// P0-SECURITY-15
// Rule 10: only a validated #rrggbb reaches theme-color and only an
// allowlisted look reaches data-look. Anything else is replaced at the
// layout by the platform default color and Clean, so a bad stored or
// passed value can't inject markup or CSS.
func TestRule10_InvalidThemeColorAndLookNeverReachThePage(t *testing.T) {
	r := newRenderer(t)
	for _, color := range []string{"red", "#12345", "#123456;}</style><script>x</script>", "", "#7c3aed\n"} {
		site := groupSite
		site.ThemeColor = color
		rec, doc := renderPage(t, r, "home_group", tmpl.Page{Site: site})
		if got, _ := attr(metaNamed(doc, "theme-color")[0], "content"); got != "#2563eb" {
			t.Errorf("ThemeColor %q: theme-color = %q, want the platform default #2563eb", color, got)
		}
		if strings.Contains(rec.Body.String(), "</style>") || strings.Contains(rec.Body.String(), "<script>x") {
			t.Errorf("ThemeColor %q reached the page", color)
		}
	}
	for _, look := range []string{"Clean", "noticeboard", `"><script>`, "", "poster "} {
		site := groupSite
		site.Look = look
		rec, doc := renderPage(t, r, "home_group", tmpl.Page{Site: site})
		if got, _ := attr(one(t, doc, "body"), "data-look"); got != "clean" {
			t.Errorf("Look %q: data-look = %q, want clean", look, got)
		}
		if n := len(elements(doc, "script")); n != 1 || strings.Contains(rec.Body.String(), `"><script>`) {
			t.Errorf("Look %q reached the page", look)
		}
	}
	// Valid values pass through unchanged.
	site := groupSite
	site.ThemeColor, site.Look = "#7C3AED", "poster"
	_, doc := renderPage(t, r, "home_group", tmpl.Page{Site: site})
	if got, _ := attr(metaNamed(doc, "theme-color")[0], "content"); got != "#7C3AED" {
		t.Errorf("valid ThemeColor became %q", got)
	}
	if got, _ := attr(one(t, doc, "body"), "data-look"); got != "poster" {
		t.Errorf("valid look became %q", got)
	}
}

// failingContent fails in the middle of execution, after the layout has
// produced output.
type failingContent struct{}

func (failingContent) Explode() (string, error) { return "", errors.New("boom") }

// Phase 0 step 6 (renderer); decisions.md "Error pages" (the caller must
// still be able to send an error page). No P0 id was given for this test.
// Render and RenderFragment write nothing (no header, no body) when a
// template fails, the page or fragment doesn't exist, or (with reload) a
// template no longer parses.
func TestRender_WritesNothingOnError(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"layouts/base.html":  `<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>`,
		"pages/bad.html":     `{{define "content"}}<p>before</p>{{.Content.Explode}}{{end}}`,
		"pages/good.html":    `{{define "content"}}<p>good</p>{{end}}`,
		"fragments/bad.html": `<p>before</p>{{.Explode}}`,
	})
	r, err := tmpl.New(dir, &fakeAssets{}, true)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, err error, w *spyWriter) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if !w.untouched() {
			t.Errorf("%s: wrote header=%v headers=%v body=%q", name, w.wroteHeader, w.header, w.body)
		}
	}
	w := newSpy()
	check("failing page", r.Render(w, 200, "bad", tmpl.Page{Content: failingContent{}}), w)
	w = newSpy()
	check("unknown page", r.Render(w, 200, "missing", tmpl.Page{}), w)
	w = newSpy()
	check("failing fragment", r.RenderFragment(w, 200, "bad", failingContent{}), w)
	w = newSpy()
	check("unknown fragment", r.RenderFragment(w, 200, "missing", nil), w)

	writeFiles(t, dir, map[string]string{"pages/good.html": `{{define "content"}}{{if}}{{end}}`})
	w = newSpy()
	check("page that no longer parses", r.Render(w, 200, "good", tmpl.Page{}), w)
}

// Phase 0 step 8 ("a forgotten route fails safe": the route layer owns
// Cache-Control). No P0 id was given for this test.
// A successful render sets the status and Content-Type and never sets
// Cache-Control.
func TestRender_NeverSetsCacheControl(t *testing.T) {
	r := newRenderer(t)
	rec := httptest.NewRecorder()
	if err := r.Render(rec, http.StatusNotFound, "error", tmpl.Page{Site: platformSite, Content: contentFor("error")}); err != nil {
		t.Fatal(err)
	}
	frag := httptest.NewRecorder()
	if err := r.RenderFragment(frag, http.StatusInternalServerError, "error", tmpl.ErrorContent{Message: "Something went wrong on our side."}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		rec  *httptest.ResponseRecorder
		code int
	}{"page": {rec, 404}, "fragment": {frag, 500}} {
		if c.rec.Code != c.code {
			t.Errorf("%s: status %d, want %d", name, c.rec.Code, c.code)
		}
		if got := c.rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", name, got)
		}
		if v, ok := c.rec.Header()["Cache-Control"]; ok {
			t.Errorf("%s: Cache-Control set to %q", name, v)
		}
	}
}

// Phase 0 step 11/12 (hikers.localhost:8080 resolves to a group context);
// design.md 1.6, 3 (nav), 8 (initials tile when there is no logo);
// prd.md 4.3 white-label. No P0 id was given for this test.
// A group's pages show the initials tile and the group's name and never
// the platform name.
func TestRender_GroupSiteShowsInitialsTileAndNameAndNoPlatformName(t *testing.T) {
	r := newRenderer(t)
	for _, page := range r.Pages() {
		rec, doc := renderPage(t, r, page, tmpl.Page{Site: groupSite, Content: contentFor(page)})
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(platformName)) {
			t.Errorf("page %q on a group site mentions the platform name", page)
		}
		nav := one(t, doc, "nav")
		if !strings.Contains(text(nav), groupSite.SiteName) {
			t.Errorf("page %q: nav lacks the group name", page)
		}
		tiles := findAll(nav, func(n *html.Node) bool {
			hidden, _ := attr(n, "aria-hidden")
			return n.Data == "span" && hidden == "true" && text(n) == groupSite.Initials
		})
		if len(tiles) != 1 {
			t.Errorf("page %q: want one initials tile with %q, found %d", page, groupSite.Initials, len(tiles))
		}
	}
	_, doc := renderPage(t, r, "home_group", tmpl.Page{Site: groupSite})
	if got := text(one(t, doc, "h1")); got != groupSite.SiteName {
		t.Errorf("group home headline = %q, want the group name", got)
	}
	if !strings.Contains(text(one(t, doc, "main")), "Meetings and posts will appear here.") {
		t.Errorf("group home: placeholder sentence missing")
	}
}

// Phase 0 step 11 (placeholder home page). No P0 id was given for this test.
// The platform's pages show the platform name, with no initials tile.
func TestRender_PlatformSiteShowsPlatformName(t *testing.T) {
	r := newRenderer(t)
	_, doc := renderPage(t, r, "home_platform", tmpl.Page{Site: platformSite})
	if got := text(one(t, doc, "h1")); got != platformName {
		t.Errorf("platform home headline = %q, want %q", got, platformName)
	}
	if !strings.Contains(text(one(t, doc, "nav")), platformName) {
		t.Errorf("nav lacks the platform name")
	}
	if spans := findAll(one(t, doc, "nav"), func(n *html.Node) bool {
		h, _ := attr(n, "aria-hidden")
		return h == "true"
	}); len(spans) != 0 {
		t.Errorf("platform nav has an initials tile")
	}
}

// decisions.md "Error pages"; design.md 6 ("One message, one way out").
// No P0 id was given for this test.
// The error page shows the caller's message and exactly one link out, on
// the platform and on a group's site.
func TestErrorPage_ShowsOneMessageAndOneWayOut(t *testing.T) {
	r := newRenderer(t)
	cases := []tmpl.ErrorContent{
		{Status: 404, Message: "We can't find that page.", LinkText: "Go to the home page", LinkHref: "/"},
		{Status: 500, Message: "Something went wrong on our side.", LinkText: "Try again", LinkHref: "/groups/hikers?x=1"},
	}
	for _, site := range []tmpl.Site{platformSite, groupSite} {
		for _, c := range cases {
			_, doc := renderPage(t, r, "error", tmpl.Page{Site: site, Content: c})
			main := one(t, doc, "main")
			links := elements(main, "a")
			if len(links) != 1 {
				t.Fatalf("%s %d: %d links in main, want one", site.Kind, c.Status, len(links))
			}
			if href, _ := attr(links[0], "href"); href != c.LinkHref {
				t.Errorf("%s %d: link href %q, want %q", site.Kind, c.Status, href, c.LinkHref)
			}
			if got := text(links[0]); got != c.LinkText {
				t.Errorf("%s %d: link text %q, want %q", site.Kind, c.Status, got, c.LinkText)
			}
			if got := text(main); got != c.Message+" "+c.LinkText {
				t.Errorf("%s %d: main says %q, want only the message and the link", site.Kind, c.Status, got)
			}
		}
	}
}

// decisions.md "Error pages" (htmx fragment requests: the one-line message
// only). No P0 id was given for this test.
// The error fragment is a single <p role="alert"> holding the message,
// with no layout around it.
func TestErrorFragment_IsOnlyTheMessage(t *testing.T) {
	r := newRenderer(t)
	rec := httptest.NewRecorder()
	msg := "We can't find that page."
	if err := r.RenderFragment(rec, http.StatusNotFound, "error", tmpl.ErrorContent{Status: 404, Message: msg, LinkText: "Go to the home page", LinkHref: "/"}); err != nil {
		t.Fatal(err)
	}
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(rec.Body.String()), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var els []*html.Node
	for _, n := range nodes {
		switch {
		case n.Type == html.ElementNode:
			els = append(els, n)
		case n.Type == html.TextNode && strings.TrimSpace(n.Data) == "":
		default:
			t.Errorf("unexpected node %q outside the message", n.Data)
		}
	}
	if len(els) != 1 || els[0].Data != "p" {
		t.Fatalf("want exactly one <p>, got %q", rec.Body.String())
	}
	if role, _ := attr(els[0], "role"); role != "alert" {
		t.Errorf("role = %q, want alert", role)
	}
	if els[0].FirstChild == nil || els[0].FirstChild != els[0].LastChild || text(els[0]) != msg {
		t.Errorf("fragment = %q, want only the message", rec.Body.String())
	}
}

// Phase 0 step 6 (development: template edits without a restart). No P0
// id was given for this test.
// With reload set, Render parses the files again on every call.
func TestRender_ReloadPicksUpEdits(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"layouts/base.html": `{{block "content" .}}{{end}}`,
		"pages/p.html":      `{{define "content"}}one{{end}}`,
	})
	r, err := tmpl.New(dir, &fakeAssets{}, true)
	if err != nil {
		t.Fatal(err)
	}
	render := func() string {
		rec := httptest.NewRecorder()
		if err := r.Render(rec, 200, "p", tmpl.Page{}); err != nil {
			t.Fatal(err)
		}
		return rec.Body.String()
	}
	if got := render(); got != "one" {
		t.Fatalf("first render %q", got)
	}
	writeFiles(t, dir, map[string]string{"pages/p.html": `{{define "content"}}two{{end}}`})
	// Keep the test independent of mtime granularity: reload must not
	// depend on it.
	_ = os.Chtimes(filepath.Join(dir, "pages", "p.html"), time.Unix(0, 0), time.Unix(0, 0))
	if got := render(); got != "two" {
		t.Errorf("after edit %q, want two", got)
	}
}
