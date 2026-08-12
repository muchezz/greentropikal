package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*.html web/static/*
var webFS embed.FS

// site renders the marketing pages and serves static assets. Templates are
// parsed and assets are fingerprinted once at startup, so a request never
// touches the filesystem.
type site struct {
	cfg    Config
	tpl    *template.Template
	assets map[string]*asset
	pages  map[string]*asset
}

type asset struct {
	body        []byte
	gz          []byte // pre-compressed at startup; nil for already-compressed types
	contentType string
	url         string
	etag        string
}

// gzipBytes compresses at the highest level once, at startup. Assets and pages
// never change at runtime, so paying for compression per request would be
// wasted work.
func gzipBytes(body []byte) []byte {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := zw.Write(body); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	// Skip it when compression does not actually help.
	if buf.Len() >= len(body) {
		return nil
	}
	return buf.Bytes()
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// compressible reports whether a content type benefits from gzip. PNGs and
// other already-compressed formats are served as-is.
func compressible(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "text/"),
		strings.Contains(contentType, "javascript"),
		strings.Contains(contentType, "json"),
		strings.Contains(contentType, "xml"),
		strings.Contains(contentType, "svg"):
		return true
	}
	return false
}

// pageData is the model shared by every template.
type pageData struct {
	Title        string
	Description  string
	Canonical    string
	BaseURL      string
	StudyZoraURL string
	ContactEmail string
	Year         int
	// HasStudyZoraLink is false until STUDYZORA_URL is configured, in which
	// case the call to action points at the on-page section rather than an
	// invented destination.
	HasStudyZoraLink bool
}

func newSite(cfg Config) (*site, error) {
	s := &site{cfg: cfg, assets: map[string]*asset{}, pages: map[string]*asset{}}

	if err := s.loadAssets(); err != nil {
		return nil, err
	}

	tpl := template.New("").Funcs(template.FuncMap{
		"asset": s.assetURL,
	})
	tpl, err := tpl.ParseFS(webFS, "web/*.html")
	if err != nil {
		return nil, err
	}
	s.tpl = tpl

	// Marketing pages are identical for every visitor, so render them once.
	for name, meta := range map[string]struct{ title, desc, canonical string }{
		"index.html": {
			"NjiraLab — Building Technology for Human Progress",
			"NjiraLab builds intelligent products and technology systems that help people learn, build, and go further.",
			"/",
		},
		"vault.html": {
			"Vault by NjiraLab — Secure secret sharing",
			"Create and share sensitive information through temporary, controlled access. Encrypted storage, automatic expiry, and one-time links.",
			"/vault",
		},
		"vault_view.html": {
			"Vault by NjiraLab",
			"",
			"",
		},
	} {
		buf := &bytes.Buffer{}
		data := s.data(meta.title, meta.desc, meta.canonical)
		if err := s.tpl.ExecuteTemplate(buf, name, data); err != nil {
			return nil, fmt.Errorf("render %s: %w", name, err)
		}
		body := buf.Bytes()
		s.pages[name] = &asset{
			body:        body,
			gz:          gzipBytes(body),
			contentType: "text/html; charset=utf-8",
		}
	}
	return s, nil
}

func (s *site) data(title, desc, canonicalPath string) pageData {
	canonical := ""
	if canonicalPath != "" {
		canonical = s.cfg.BaseURL + strings.TrimSuffix(canonicalPath, "/")
		if canonicalPath == "/" {
			canonical = s.cfg.BaseURL + "/"
		}
	}
	studyzora := s.cfg.StudyZoraURL
	return pageData{
		Title:            title,
		Description:      desc,
		Canonical:        canonical,
		BaseURL:          s.cfg.BaseURL,
		StudyZoraURL:     firstNonEmpty(studyzora, "#studyzora"),
		ContactEmail:     s.cfg.ContactEmail,
		Year:             time.Now().Year(),
		HasStudyZoraLink: studyzora != "",
	}
}

func (s *site) loadAssets() error {
	entries, err := fs.ReadDir(webFS, "web/static")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := webFS.ReadFile("web/static/" + e.Name())
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])[:12]

		ct := mime.TypeByExtension(path.Ext(e.Name()))
		if ct == "" {
			ct = "application/octet-stream"
		}
		a := &asset{
			body:        body,
			contentType: ct,
			// Content-hashed URLs let assets be cached forever and still
			// update the instant a file changes.
			url:  "/static/" + e.Name() + "?v=" + digest,
			etag: `"` + digest + `"`,
		}
		if compressible(ct) {
			a.gz = gzipBytes(body)
		}
		s.assets[e.Name()] = a
	}
	return nil
}

func (s *site) assetURL(name string) string {
	if a, ok := s.assets[name]; ok {
		return a.url
	}
	return "/static/" + name
}

func (s *site) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.page("index.html"))
	mux.HandleFunc("GET /vault", s.page("vault.html"))
	mux.HandleFunc("GET /s/{id}", s.secretView)
	mux.HandleFunc("GET /static/{name}", s.static)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)

	// Links created before the rebrand keep working.
	mux.HandleFunc("GET /secrets", redirect("/vault"))
	mux.HandleFunc("GET /secret/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !idPattern.MatchString(id) {
			s.notFound(w)
			return
		}
		http.Redirect(w, r, "/s/"+id, http.StatusMovedPermanently)
	})
}

func (s *site) page(name string) http.HandlerFunc {
	a := s.pages[name]
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		writeAsset(w, r, a)
	}
}

// writeAsset serves a pre-rendered body, using the gzipped copy when the
// client accepts it.
func writeAsset(w http.ResponseWriter, r *http.Request, a *asset) {
	h := w.Header()
	h.Set("Content-Type", a.contentType)
	h.Add("Vary", "Accept-Encoding")

	body := a.body
	if a.gz != nil && acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		body = a.gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// secretView serves the reader page. The identifier is never interpolated into
// the markup — the page reads it from its own URL — so a crafted path cannot
// inject anything. An invalid shape is rejected outright.
func (s *site) secretView(w http.ResponseWriter, r *http.Request) {
	if !idPattern.MatchString(r.PathValue("id")) {
		s.notFound(w)
		return
	}
	noStore(w)
	// Secret pages must never be indexed or archived by crawlers.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	writeAsset(w, r, s.pages["vault_view.html"])
}

func (s *site) static(w http.ResponseWriter, r *http.Request) {
	a, ok := s.assets[r.PathValue("name")]
	if !ok {
		s.notFound(w)
		return
	}
	h := w.Header()
	h.Set("ETag", a.etag)
	if r.URL.Query().Get("v") != "" {
		// Fingerprinted URL: safe to cache forever.
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "public, max-age=3600")
	}
	if match := r.Header.Get("If-None-Match"); match == a.etag {
		h.Add("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeAsset(w, r, a)
}

func (s *site) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /$\nAllow: /vault\nDisallow: /s/\nDisallow: /api/\n\nSitemap: %s/sitemap.xml\n", s.cfg.BaseURL)
}

func (s *site) sitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/</loc><priority>1.0</priority></url>
  <url><loc>%s/vault</loc><priority>0.8</priority></url>
</urlset>
`, s.cfg.BaseURL, s.cfg.BaseURL)
}

func (s *site) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("Not found"))
}

func redirect(to string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to, http.StatusMovedPermanently)
	}
}
