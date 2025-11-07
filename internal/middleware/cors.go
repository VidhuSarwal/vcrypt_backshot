package middleware

import (
	"net/http"
	"strings"
	"time"
)

// CORS returns a middleware that sets permissive CORS headers based on allowed origins.
// Pass []string{"*"} to allow all origins (default now). Later, replace with specific origins like
// []string{"http://localhost:3000", "https://yourapp.com"}.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
    // Normalize allowed origins once
    norm := make([]string, 0, len(allowedOrigins))
    hasWildcard := false
    for _, o := range allowedOrigins {
        o = strings.TrimSpace(o)
        if o == "*" {
            hasWildcard = true
        }
        if o != "" {
            norm = append(norm, o)
        }
    }

    allowMethods := "GET, POST, PUT, PATCH, DELETE, OPTIONS"
    // Typical headers used by browsers and APIs; during preflight we mirror the request headers when provided
    defaultAllowHeaders := "Authorization, Content-Type, Accept, X-Requested-With"
    maxAge := 24 * time.Hour

    originAllowed := func(origin string) bool {
        if origin == "" {
            return false
        }
        if hasWildcard {
            return true
        }
        for _, o := range norm {
            if strings.EqualFold(o, origin) {
                return true
            }
        }
        return false
    }

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            origin := r.Header.Get("Origin")

            // Always vary on these so proxies don't cache incorrectly
            w.Header().Add("Vary", "Origin")
            w.Header().Add("Vary", "Access-Control-Request-Method")
            w.Header().Add("Vary", "Access-Control-Request-Headers")

            if originAllowed(origin) {
                // If wildcard is used and credentials are NOT used, we can safely return "*"
                if hasWildcard {
                    w.Header().Set("Access-Control-Allow-Origin", "*")
                } else {
                    // Echo back the requesting origin when doing an allowlist
                    w.Header().Set("Access-Control-Allow-Origin", origin)
                }
                // Not enabling credentials by default. If you need credentials, set this to true and
                // ensure you DO NOT use wildcard origins (browsers block that combination).
                // w.Header().Set("Access-Control-Allow-Credentials", "true")

                // Preflight handling
                if r.Method == http.MethodOptions {
                    reqMethod := r.Header.Get("Access-Control-Request-Method")
                    if reqMethod != "" {
                        w.Header().Set("Access-Control-Allow-Methods", allowMethods)
                        reqHeaders := r.Header.Get("Access-Control-Request-Headers")
                        if reqHeaders != "" {
                            w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
                        } else {
                            w.Header().Set("Access-Control-Allow-Headers", defaultAllowHeaders)
                        }
                        w.Header().Set("Access-Control-Max-Age", toSeconds(maxAge))
                        w.WriteHeader(http.StatusNoContent)
                        return
                    }
                }
            }

            next.ServeHTTP(w, r)
        })
    }
}

func toSeconds(d time.Duration) string {
    return strconvFormatInt(int64(d/time.Second))
}

func strconvFormatInt(i int64) string {
    // Avoid importing strconv just for one tiny use; implement a minimal int->string.
    // This is fine for our small numbers like seconds values.
    if i == 0 {
        return "0"
    }
    neg := false
    if i < 0 {
        neg = true
        i = -i
    }
    var b [20]byte
    bp := len(b)
    for i > 0 {
        bp--
        b[bp] = byte('0' + i%10)
        i /= 10
    }
    if neg {
        bp--
        b[bp] = '-'
    }
    return string(b[bp:])
}
