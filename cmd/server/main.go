package main

import (
	"SE/internal/auth"
	"SE/internal/filehandlers"
	"SE/internal/fileprocessor"
	"SE/internal/handlers"
	"SE/internal/oauth"
	"SE/internal/store"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	// Load env vars
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found")
	}

	// Check required env vars
	required := []string{"MONGO_URI", "JWT_SECRET", "TOKEN_ENC_KEY", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "BASE_URL"}
	for _, k := range required {
		if os.Getenv(k) == "" {
			log.Fatalf("env %s is required", k)
		}
	}

	// Initialize store (Mongo)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.InitStore(ctx); err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.DisconnectStore(ctx); err != nil {
			log.Printf("disconnect store: %v", err)
		}
	}()

	// Initialize oauth config
	oauth.InitOAuthConfig()

	// Initialize file processor config
	fileprocessor.InitFileConfig()

	// Setup routes
	mux := http.NewServeMux()

	// Authentication routes
	mux.HandleFunc("/api/signup", requireMethod("POST", auth.SignupHandler))
	mux.HandleFunc("/api/login", requireMethod("POST", auth.LoginHandler))

	// Drive OAuth routes
	mux.HandleFunc("/api/drive/link", auth.AuthMiddleware(requireMethod("GET", oauth.DriveLinkHandler)))
	mux.HandleFunc("/api/drive/accounts", auth.AuthMiddleware(requireMethod("GET", handlers.ListDriveAccountsHandler)))
	mux.HandleFunc("/api/drive/space", auth.AuthMiddleware(requireMethod("GET", filehandlers.GetDriveSpacesHandler)))

	// File upload routes
	mux.HandleFunc("/api/files/upload/initiate", auth.AuthMiddleware(requireMethod("POST", filehandlers.InitiateUploadHandler)))
	mux.HandleFunc("/api/files/upload/chunk", auth.AuthMiddleware(requireMethod("POST", filehandlers.UploadChunkHandler)))
	mux.HandleFunc("/api/files/upload/finalize", auth.AuthMiddleware(requireMethod("POST", filehandlers.FinalizeUploadHandler)))
	mux.HandleFunc("/api/files/upload/status/", auth.AuthMiddleware(requireMethod("GET", filehandlers.GetUploadStatusHandler)))
	mux.HandleFunc("/api/files/chunking/calculate", auth.AuthMiddleware(requireMethod("POST", filehandlers.CalculateChunkingHandler)))

	// OAuth callback (no auth header; state validated via DB)
	mux.HandleFunc("/oauth2/callback", requireMethod("GET", oauth.OauthCallbackHandler))

	// OAuth completion page
	mux.HandleFunc("/oauth/finished", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<h1>OAuth flow completed</h1><p>You can close this window and return to the application.</p>"))
	})

	addr := ":8080"
	fmt.Printf("Starting server on %s\n", addr)
	if err := http.ListenAndServe(addr, logRequest(mux)); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func requireMethod(verb string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != verb {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// logRequest is a global middleware that:
// - Sets permissive CORS headers (development)
// - Logs incoming request meta and a safe snapshot of the body (masked, truncated)
// - Captures and logs outgoing status and a safe snapshot of the response body
func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// --- CORS handling ---
		reqOrigin := r.Header.Get("Origin")
		allowOrigins := os.Getenv("CORS_ALLOW_ORIGINS") // comma-separated; if empty, default "*"
		allowCreds := os.Getenv("CORS_ALLOW_CREDENTIALS") == "true"

		// Decide allowed origin
		allowedOrigin := "*"
		if allowOrigins != "" && reqOrigin != "" {
			// allow only if reqOrigin is in the allowlist
			for _, o := range strings.Split(allowOrigins, ",") {
				o = strings.TrimSpace(o)
				if o == reqOrigin {
					allowedOrigin = reqOrigin
					break
				}
			}
		} else if reqOrigin != "" && allowCreds {
			// If credentials are requested, cannot use "*"; reflect the origin
			allowedOrigin = reqOrigin
		}

		// Always set Vary for correct caching behavior
		w.Header().Add("Vary", "Origin")
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")

		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		// If browser asked for specific headers, echo them back; else use sensible defaults
		if reqHdrs := r.Header.Get("Access-Control-Request-Headers"); reqHdrs != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHdrs)
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, X-Requested-With")
		}

		if allowCreds && allowedOrigin != "*" {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		// Optional: cache preflight for 10 minutes
		w.Header().Set("Access-Control-Max-Age", "600")

		// Preflight short-circuit
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

	const maxLogBytes = 4096 // cap body capture per direction

		// Masked headers of interest
		authHeader := maskAuthorization(r.Header.Get("Authorization"))
		contentType := r.Header.Get("Content-Type")

		// Wrap request body to capture safely for JSON/form only (skip multipart and large uploads)
		var reqBodyBuf limitedBuffer
		reqBodyBuf.max = maxLogBytes
		originalBody := r.Body
		if isLoggableContentType(contentType) && originalBody != nil {
			r.Body = newTeeReadCloser(originalBody, &reqBodyBuf)
		}

		// Wrap response writer to capture status and body
		rr := &respRecorder{ResponseWriter: w, status: http.StatusOK, max: maxLogBytes}

		// Call next
		start := time.Now()
		next.ServeHTTP(rr, r)
		dur := time.Since(start)

		// Build safe request body preview
		reqPreview := safeBodyPreview(reqBodyBuf.Bytes(), contentType)
		if strings.Contains(contentType, "password") { // not typical, extra guard
			reqPreview = maskSensitive(reqPreview)
		}

		// Build safe query string preview
		qPreview := maskQuery(r.URL.Query())

		// Build safe response body preview (assume JSON commonly)
		respCT := rr.Header().Get("Content-Type")
		respPreview := safeBodyPreview(rr.body.Bytes(), respCT)

		// Log line
		log.Printf("%s %s from %s | ct=%q auth=%q | %d %s in %s\nquery: %s\nreq: %s\nresp: %s\n",
			r.Method,
			r.URL.RequestURI(),
			r.RemoteAddr,
			contentType,
			authHeader,
			rr.status,
			http.StatusText(rr.status),
			dur,
			qPreview,
			reqPreview,
			respPreview,
		)
	})
}

// --- helpers for logging ---

type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	remain := l.max - l.buf.Len()
	if remain <= 0 {
		// discard
		return len(p), nil
	}
	if len(p) > remain {
		l.buf.Write(p[:remain])
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) Bytes() []byte { return l.buf.Bytes() }

type teeReadCloser struct {
	r io.Reader
	c io.Closer
}

func newTeeReadCloser(rc io.ReadCloser, w io.Writer) io.ReadCloser {
	return &teeReadCloser{r: io.TeeReader(rc, w), c: rc}
}

func (t *teeReadCloser) Read(p []byte) (int, error) { return t.r.Read(p) }
func (t *teeReadCloser) Close() error              { return t.c.Close() }

type respRecorder struct {
	http.ResponseWriter
	status int
	body   limitedBuffer
	max    int
}

func (r *respRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *respRecorder) Write(p []byte) (int, error) {
	// copy into limited buffer for logging
	if r.body.max == 0 {
		r.body.max = r.max
	}
	_, _ = r.body.Write(p)
	return r.ResponseWriter.Write(p)
}

// Support Flusher passthrough if underlying supports it
func (r *respRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func isLoggableContentType(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "multipart/") { // skip file uploads
		return false
	}
	if strings.Contains(ct, "application/json") || strings.Contains(ct, "application/x-www-form-urlencoded") {
		return true
	}
	return false
}

func safeBodyPreview(b []byte, ct string) string {
	if len(b) == 0 {
		return "<empty>"
	}
	// Only pretty-print or mask for JSON; otherwise show raw (truncated by limitedBuffer)
	s := string(b)
	if strings.Contains(strings.ToLower(ct), "application/json") {
		s = maskSensitive(s)
	}
	// collapse newlines for compact logs
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func maskAuthorization(h string) string {
	if h == "" { return "" }
	// Expect formats: "Bearer <token>" or others
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 { return "<masked>" }
	tok := parts[1]
	if len(tok) <= 8 { return parts[0] + " ****" }
	return parts[0] + " " + tok[:4] + "****" + tok[len(tok)-4:]
}

var sensitiveFields = regexp.MustCompile(`(?i)\"(password|token|access_token|refresh_token|authorization)\"\s*:\s*\"[^\"]*\"`)

func maskSensitive(s string) string {
	return sensitiveFields.ReplaceAllStringFunc(s, func(m string) string {
		// Replace the value part with masked value
		i := strings.Index(m, ":")
		if i == -1 { return m }
		key := m[:i]
		return key + ": \"****\""
	})
}

var sensitiveQueryKeys = regexp.MustCompile(`(?i)^(password|pass|token|access_token|refresh_token|authorization|auth|secret)$`)

// maskQuery renders a concise, masked summary of URL query parameters.
// Example output: "email=user@example.com&user_id=507f...&token=****"
func maskQuery(q url.Values) string {
	if len(q) == 0 {
		return "<none>"
	}
	const maxLen = 1024
	var b strings.Builder
	first := true
	for k, vals := range q {
		if !first {
			b.WriteByte('&')
		}
		first = false
		b.WriteString(k)
		b.WriteByte('=')
		if len(vals) == 0 {
			continue
		}
		v := vals[0]
		if sensitiveQueryKeys.MatchString(k) {
			b.WriteString("****")
		} else {
			// limit individual value length to avoid spammy logs
			if len(v) > 120 { v = v[:120] + "…" }
			// collapse whitespace
			v = strings.ReplaceAll(v, "\n", " ")
			b.WriteString(v)
		}
		// Early truncate entire summary if too long
		if b.Len() > maxLen {
			return b.String()[:maxLen] + "…"
		}
	}
	return b.String()
}
