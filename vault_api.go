package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"
)

// Vault serves the secret-sharing API.
type Vault struct {
	store    *Store
	baseURL  string
	originOK func(*http.Request) bool
}

const (
	maxBodyBytes   = 96 << 10 // request ceiling, leaves room for JSON overhead
	maxSecretBytes = 64 << 10 // largest secret we accept
	minTTL         = 5 * time.Minute
	maxTTL         = 30 * 24 * time.Hour
	maxViewsLimit  = 10
)

// idPattern matches the base64url identifiers produced by newID. Validating
// the shape before touching Redis keeps malformed input out of key names and
// out of the page template.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

var secretKinds = map[string]bool{
	"api-key":     true,
	"password":    true,
	"token":       true,
	"credentials": true,
	"config":      true,
	"other":       true,
}

func (v *Vault) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/create", v.handleCreate)
	mux.HandleFunc("POST /api/check", v.handleCheck)
	mux.HandleFunc("POST /api/view", v.handleView)
	mux.HandleFunc("POST /api/burn", v.handleBurn)
	mux.HandleFunc("GET /api/health", v.handleHealth)
}

type createRequest struct {
	Secret     string `json:"secret"`
	Type       string `json:"type"`
	ExpiryTime int    `json:"expiry_time"` // seconds
	MaxViews   int    `json:"max_views"`
}

type createResponse struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	MaxViews  int    `json:"max_views"`
}

func (v *Vault) handleCreate(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !v.originOK(r) {
		writeErr(w, http.StatusForbidden, "Cross-origin requests are not allowed.")
		return
	}

	var req createRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if req.Secret == "" {
		writeErr(w, http.StatusBadRequest, "Enter the information you want to share.")
		return
	}
	if len(req.Secret) > maxSecretBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "That secret is too large. The limit is 64 KB.")
		return
	}

	ttl := time.Duration(req.ExpiryTime) * time.Second
	if ttl < minTTL || ttl > maxTTL {
		writeErr(w, http.StatusBadRequest, "Choose an expiry between 5 minutes and 30 days.")
		return
	}
	if req.MaxViews < 1 || req.MaxViews > maxViewsLimit {
		writeErr(w, http.StatusBadRequest, "Choose between 1 and 10 views.")
		return
	}
	kind := req.Type
	if !secretKinds[kind] {
		kind = "other"
	}

	key, err := newKey()
	if err != nil {
		serverErr(w)
		return
	}
	ciphertext, err := encrypt(key, []byte(req.Secret))
	if err != nil {
		serverErr(w)
		return
	}
	id, err := newID()
	if err != nil {
		serverErr(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := v.store.Put(ctx, id, ciphertext, kind, req.MaxViews, ttl); err != nil {
		// Never hand back a link for a secret that was not stored.
		writeErr(w, http.StatusServiceUnavailable, "Storage is unavailable right now. Nothing was saved — please try again.")
		return
	}

	// The key rides in the fragment, which browsers keep out of requests. The
	// server has now discarded its copy.
	writeJSON(w, http.StatusOK, createResponse{
		ID:        id,
		URL:       v.baseURL + "/s/" + id + "#" + b64.EncodeToString(key),
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
		MaxViews:  req.MaxViews,
	})
}

type idRequest struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// handleCheck reports whether a secret is still available without spending a
// view, so the viewer page can show its state before the reader commits.
func (v *Vault) handleCheck(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	var req idRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !idPattern.MatchString(req.ID) {
		writeErr(w, http.StatusNotFound, "This link is not valid.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rec, err := v.store.Peek(ctx, req.ID)
	if err != nil {
		v.writeLookupErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"type":       rec.Kind,
		"view_count": rec.Views,
		"max_views":  rec.MaxViews,
		"expires_at": rec.ExpiresAt.Format(time.RFC3339),
	})
}

type viewResponse struct {
	Secret    string `json:"secret"`
	Type      string `json:"type"`
	ViewCount int    `json:"view_count"`
	MaxViews  int    `json:"max_views"`
}

func (v *Vault) handleView(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !v.originOK(r) {
		writeErr(w, http.StatusForbidden, "Cross-origin requests are not allowed.")
		return
	}

	var req idRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !idPattern.MatchString(req.ID) {
		writeErr(w, http.StatusNotFound, "This link is not valid.")
		return
	}
	key, err := decodeKey(req.Key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "This link is missing its decryption key. Use the complete link exactly as it was shared with you.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Read metadata and ciphertext first, then decrypt, and only spend a view
	// once we know the key is right. A wrong or truncated key must not burn a
	// view that the intended recipient still needs.
	rec, err := v.store.Peek(ctx, req.ID)
	if err != nil {
		v.writeLookupErr(w, err)
		return
	}
	ciphertext, err := v.store.Ciphertext(ctx, req.ID)
	if err != nil {
		v.writeLookupErr(w, err)
		return
	}
	plaintext, err := decrypt(key, ciphertext)
	if err != nil {
		writeErr(w, http.StatusForbidden, "This link's key does not match this secret.")
		return
	}

	used, err := v.store.Consume(ctx, req.ID)
	if err != nil {
		// Another reader took the last view between the two calls.
		v.writeLookupErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, viewResponse{
		Secret:    string(plaintext),
		Type:      firstNonEmpty(rec.Kind, "other"),
		ViewCount: used,
		MaxViews:  rec.MaxViews,
	})
}

func (v *Vault) handleBurn(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !v.originOK(r) {
		writeErr(w, http.StatusForbidden, "Cross-origin requests are not allowed.")
		return
	}

	var req idRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !idPattern.MatchString(req.ID) {
		writeErr(w, http.StatusNotFound, "This link is not valid.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := v.store.Burn(ctx, req.ID); err != nil {
		v.writeLookupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "destroyed"})
}

// handleHealth reports liveness only. It deliberately exposes no counts: the
// number of stored secrets is operational information about our users.
func (v *Vault) handleHealth(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status, code := "ok", http.StatusOK
	if err := v.store.Ping(ctx); err != nil {
		status, code = "degraded", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{
		"status": status,
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (v *Vault) writeLookupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		// One message for expired, exhausted, burned and never-existed, so the
		// response cannot be used to tell those cases apart.
		writeErr(w, http.StatusNotFound, "This secret is no longer available. It may have expired, reached its view limit, or already been destroyed.")
		return
	}
	writeErr(w, http.StatusServiceUnavailable, "Storage is unavailable right now. Please try again shortly.")
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "We could not read that request.")
		return false
	}
	return true
}

// writeJSON deliberately does not compress. Static pages and assets are gzipped
// at startup, but API responses carry secret plaintext, and compressing a
// response that mixes secret and caller-influenced data is the setup for a
// BREACH-style length attack.
func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErr returns errors as JSON so the client never renders a raw server
// string, and so no request detail is echoed back into the page.
func writeErr(w http.ResponseWriter, code int, message string) {
	noStore(w)
	writeJSON(w, code, map[string]string{"error": message})
}

func serverErr(w http.ResponseWriter) {
	writeErr(w, http.StatusInternalServerError, "Something went wrong on our side. Please try again.")
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
