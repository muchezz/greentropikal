// Command njiralab serves the NjiraLabs website and the Vault secret-sharing
// product from a single binary. Pages and static assets are embedded, so the
// container image only needs the executable.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config holds every runtime knob. Everything has a development default so the
// binary runs with no environment set, but production deployments should set
// BASE_URL and ENABLE_HSTS at minimum.
type Config struct {
	Addr           string
	BaseURL        string
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	StudyZoraURL   string
	ContactEmail   string
	AllowedOrigins []string
	TrustProxy     bool
	EnableHSTS     bool
}

func loadConfig() Config {
	cfg := Config{
		Addr:         ":" + env("PORT", "8080"),
		BaseURL:      strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		StudyZoraURL: strings.TrimSpace(os.Getenv("STUDYZORA_URL")),
		ContactEmail: env("CONTACT_EMAIL", "hello@njiralabs.com"),
		TrustProxy:   env("TRUST_PROXY", "false") == "true",
		EnableHSTS:   env("ENABLE_HSTS", "false") == "true",
	}

	// REDIS_URL accepts either a bare host:port or a full redis:// URL.
	raw := env("REDIS_URL", "localhost:6379")
	if strings.Contains(raw, "://") {
		opts, err := redis.ParseURL(raw)
		if err != nil {
			log.Fatalf("invalid REDIS_URL: %v", err)
		}
		cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB = opts.Addr, opts.Password, opts.DB
	} else {
		cfg.RedisAddr = raw
		cfg.RedisPassword = os.Getenv("REDIS_PASSWORD")
		if n, err := strconv.Atoi(env("REDIS_DB", "0")); err == nil {
			cfg.RedisDB = n
		}
	}

	// Cross-origin API access is opt-in. Left unset, no CORS headers are sent
	// and only same-origin pages can call the Vault API.
	for _, o := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" && o != "*" {
			cfg.AllowedOrigins = append(cfg.AllowedOrigins, strings.TrimRight(o, "/"))
		}
	}
	return cfg
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := loadConfig()

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer rdb.Close()

	if cfg.StudyZoraURL == "" {
		log.Print("notice: STUDYZORA_URL is not set — the StudyZora call to action falls back to the contact section")
	}

	site, err := newSite(cfg)
	if err != nil {
		log.Fatalf("failed to prepare site: %v", err)
	}
	vault := &Vault{
		store:    &Store{rdb: rdb},
		baseURL:  cfg.BaseURL,
		originOK: sameOrigin(cfg.BaseURL, cfg.AllowedOrigins),
	}

	mux := http.NewServeMux()
	site.routes(mux)
	vault.routes(mux)

	handler := chain(mux,
		securityHeaders(cfg.EnableHSTS),
		cors(cfg.AllowedOrigins),
		rateLimit(newLimiter(60, time.Minute), cfg.TrustProxy),
	)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
		// Requests are never logged with their paths, so a secret ID can't end
		// up in the error log via the default logger either.
		ErrorLog: log.New(os.Stderr, "http: ", log.LstdFlags),
	}

	go func() {
		log.Printf("njiralab listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

type middleware func(http.Handler) http.Handler

func chain(h http.Handler, mw ...middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// securityHeaders applies a strict policy site-wide. The CSP allows no inline
// script or style, which is why every page loads its CSS and JS from /static.
func securityHeaders(hsts bool) middleware {
	const csp = "default-src 'none'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self' data:; " +
		"font-src 'self'; " +
		"connect-src 'self'; " +
		"form-action 'none'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			// no-referrer keeps secret URLs out of the Referer header entirely.
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func cors(allowed []string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && len(allowed) > 0 {
				for _, a := range allowed {
					if strings.EqualFold(a, origin) {
						h := w.Header()
						h.Set("Access-Control-Allow-Origin", origin)
						h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
						h.Set("Access-Control-Allow-Headers", "Content-Type")
						h.Set("Access-Control-Max-Age", "600")
						h.Add("Vary", "Origin")
						break
					}
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rateLimit(l *limiter, trustProxy bool) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only the API is metered; static assets and pages are cheap.
			if strings.HasPrefix(r.URL.Path, "/api/") && !l.allow(clientIP(r, trustProxy)) {
				w.Header().Set("Retry-After", "60")
				writeErr(w, http.StatusTooManyRequests, "Too many requests. Please slow down and try again shortly.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, ok := strings.Cut(xff, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin rejects state-changing API calls whose Origin header points at
// another site. Combined with the default no-CORS policy this blocks
// cross-site requests even from browsers that would send them.
func sameOrigin(baseURL string, allowed []string) func(*http.Request) bool {
	hosts := map[string]bool{}
	add := func(raw string) {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			hosts[strings.ToLower(u.Host)] = true
		}
	}
	add(baseURL)
	for _, a := range allowed {
		add(a)
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser client, or a same-origin request without Origin
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return hosts[strings.ToLower(u.Host)] || strings.EqualFold(u.Host, r.Host)
	}
}
