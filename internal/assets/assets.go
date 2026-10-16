// Package assets serves the files under static/ and builds their
// fingerprinted URLs (/static/<name>?v=<hash>), so a changed file gets a
// new URL and an unchanged one can be cached for a year.
//
// Font files are fingerprinted by name instead (figtree-variable.<hash8>.woff2),
// because output.css refers to them by a fixed URL.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// prefix is the URL path every asset is served under.
const prefix = "/static/"

// versionLen is how many hex digits of the content's sha256 go in ?v=.
const versionLen = 10

// Assets knows the files under one directory. It is safe for concurrent use.
type Assets struct {
	root *os.Root // every read goes through it: nothing outside dir is reachable
	dev  bool

	mu    sync.Mutex
	files map[string]fileVersion // by name relative to dir, slash-separated
}

type fileVersion struct {
	size    int64
	modTime time.Time
	version string
}

// New hashes every regular file under dir (skipping dotfiles and dot
// directories). With dev set, URL re-hashes a file whenever its size or
// modification time changes, so a rebuilt output.css gets a new URL
// without a restart; otherwise the versions are fixed at startup.
func New(dir string, dev bool) (*Assets, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("assets: %w", err)
	}
	a := &Assets{root: root, dev: dev, files: map[string]fileVersion{}}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fv, ok, err := a.version(name)
		if err != nil {
			return err
		}
		if ok {
			a.files[name] = fv
		}
		return nil
	})
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("assets: %w", err)
	}
	return a, nil
}

// URL returns "/static/<name>?v=<first 10 hex digits of sha256(content)>".
// For a name it doesn't know (missing, a directory, a dotfile, outside the
// directory) it logs a warning and returns "/static/<name>" unversioned.
func (a *Assets) URL(name string) string {
	plain := prefix + name
	if !validName(name) {
		slog.Warn("assets: unknown asset", "name", name)
		return plain
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fv, ok := a.files[name]
	if a.dev {
		st, err := a.root.Stat(name)
		switch {
		case err != nil || !st.Mode().IsRegular():
			ok = false
		case !ok || st.Size() != fv.size || !st.ModTime().Equal(fv.modTime):
			var err error
			fv, ok, err = a.version(name)
			if err != nil {
				slog.Warn("assets: can't hash asset", "name", name, "err", err)
				ok = false
			}
			if ok {
				a.files[name] = fv
			}
		}
	}
	if !ok {
		slog.Warn("assets: unknown asset", "name", name)
		return plain
	}
	return plain + "?v=" + fv.version
}

// version hashes one file. ok is false if name is not a regular file
// inside the root (a symlink pointing out of it, for example).
func (a *Assets) version(name string) (fv fileVersion, ok bool, err error) {
	f, err := a.root.Open(name)
	if err != nil {
		return fileVersion{}, false, nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return fileVersion{}, false, nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fileVersion{}, false, fmt.Errorf("hash %s: %w", name, err)
	}
	return fileVersion{
		size:    st.Size(),
		modTime: st.ModTime(),
		version: hex.EncodeToString(h.Sum(nil))[:versionLen],
	}, true, nil
}

// Handler serves the files. It expects r.URL.Path to start with /static/
// and ignores the query string (the ?v= is only there to change the URL).
// Directories, missing files, dotfiles and any path with a segment that
// starts with "." get a bare 404. Only regular files inside the directory
// are served: it is opened with os.OpenRoot, so ../ and symlinks that
// point outside it fail.
//
// Cache-Control and X-Content-Type-Options are left to the route and
// middleware layers. Method filtering is the route table's job (a
// "GET /static/" pattern also matches HEAD); GET and HEAD are answered
// here, HEAD without a body.
func (a *Assets) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok || !validName(name) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f, err := a.root.Open(name)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType(name))
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
}

// contentType is the Content-Type sent for a file, chosen by extension.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// validName reports whether name is a slash-separated relative path with
// no empty segments and no segment starting with "." (so no ".", "..",
// dotfiles or dot directories).
func validName(name string) bool {
	if name == "" {
		return false
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" || seg[0] == '.' {
			return false
		}
	}
	return true
}
