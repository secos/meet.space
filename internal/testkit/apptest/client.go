package apptest

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Client sends requests to the in-process app as a browser on one or more
// hosts would. It keeps one cookie jar per exact host (port included), so
// a cookie set on one host is only ever sent back to that host. Requests
// never touch the network: they go straight to the app's handler.
type Client struct {
	t    testing.TB
	h    http.Handler
	jars map[string]*cookiejar.Jar
	// Shared, when set, is a single browser-like jar used for every host,
	// for tests that must see a cookie wrongly scoped to another host.
	Shared *cookiejar.Jar
}

// Client returns a fresh client with empty cookie jars.
func (a *App) Client() *Client {
	return &Client{t: a.T, h: a.App.Handler, jars: map[string]*cookiejar.Jar{}}
}

// Response is a finished response with its body read.
type Response struct {
	*http.Response
	Body string
}

// ReqOption adjusts one request.
type ReqOption func(r *http.Request)

// WithOrigin sets the Origin header (overriding the default).
func WithOrigin(origin string) ReqOption {
	return func(r *http.Request) { r.Header.Set("Origin", origin) }
}

// NoOrigin removes the default Origin header.
func NoOrigin() ReqOption {
	return func(r *http.Request) { r.Header.Del("Origin") }
}

// WithReferer sets the Referer header.
func WithReferer(ref string) ReqOption {
	return func(r *http.Request) { r.Header.Set("Referer", ref) }
}

// WithHeader adds a header value.
func WithHeader(key, value string) ReqOption {
	return func(r *http.Request) { r.Header.Add(key, value) }
}

// WithRemoteAddr sets the immediate peer's address ("ip:port").
func WithRemoteAddr(addr string) ReqOption {
	return func(r *http.Request) { r.RemoteAddr = addr }
}

// HX marks the request as an htmx request.
func HX() ReqOption { return WithHeader("HX-Request", "true") }

// Form sends values as an application/x-www-form-urlencoded body.
func Form(values url.Values) ReqOption {
	return func(r *http.Request) {
		body := values.Encode()
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
}

// Get sends a GET to path on host.
func (c *Client) Get(host, path string, opts ...ReqOption) *Response {
	return c.Do(http.MethodGet, host, path, opts...)
}

// Post sends a POST to path on host.
func (c *Client) Post(host, path string, opts ...ReqOption) *Response {
	return c.Do(http.MethodPost, host, path, opts...)
}

// Do sends a request the way a browser on host would: Host is host, and a
// POST or DELETE carries Origin http://host unless an option changes it.
func (c *Client) Do(method, host, path string, opts ...ReqOption) *Response {
	c.t.Helper()
	req := httptest.NewRequest(method, "http://example.invalid"+path, nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:51000" // TEST-NET-1
	if method == http.MethodPost || method == http.MethodDelete {
		req.Header.Set("Origin", "http://"+host)
	}
	jarURL := &url.URL{Scheme: "https", Host: host, Path: req.URL.Path}
	jar := c.jar(host)
	for _, ck := range jar.Cookies(jarURL) {
		req.AddCookie(ck)
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	jar.SetCookies(jarURL, res.Cookies())
	return &Response{Response: res, Body: string(body)}
}

func (c *Client) jar(host string) *cookiejar.Jar {
	if c.Shared != nil {
		return c.Shared
	}
	j, ok := c.jars[host]
	if !ok {
		j, _ = cookiejar.New(nil)
		c.jars[host] = j
	}
	return j
}
