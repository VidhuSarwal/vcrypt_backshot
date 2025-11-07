package middleware

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// loggingResponseWriter wraps http.ResponseWriter to capture status code and size
type loggingResponseWriter struct {
    http.ResponseWriter
    statusCode int
    bytesWritten int
    bodyBuf      *bytes.Buffer
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
    lrw.statusCode = code
    lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
    // If Write is called without WriteHeader, assume 200
    if lrw.statusCode == 0 {
        lrw.statusCode = http.StatusOK
    }
    // Copy into body buffer with truncation protection
    if lrw.bodyBuf != nil {
        // Only keep up to responseLogLimit bytes
        if lrw.bodyBuf.Len() < responseLogLimit {
            remaining := responseLogLimit - lrw.bodyBuf.Len()
            if remaining > 0 {
                if len(b) <= remaining {
                    lrw.bodyBuf.Write(b)
                } else {
                    lrw.bodyBuf.Write(b[:remaining])
                }
            }
        }
    }
    n, err := lrw.ResponseWriter.Write(b)
    lrw.bytesWritten += n
    return n, err
}

// Support http.Flusher when the underlying writer supports it
func (lrw *loggingResponseWriter) Flush() {
    if f, ok := lrw.ResponseWriter.(http.Flusher); ok {
        f.Flush()
    }
}

// Support http.Hijacker when the underlying writer supports it
func (lrw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    if h, ok := lrw.ResponseWriter.(http.Hijacker); ok {
        return h.Hijack()
    }
    return nil, nil, http.ErrNotSupported
}

// Support http.Pusher (HTTP/2) when the underlying writer supports it
func (lrw *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
    if p, ok := lrw.ResponseWriter.(http.Pusher); ok {
        return p.Push(target, opts)
    }
    return http.ErrNotSupported
}

// Logger returns a middleware that logs request method, path, response status, size and duration.
func Logger(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        // Prepare response writer wrapper
        lrw := &loggingResponseWriter{ResponseWriter: w, bodyBuf: &bytes.Buffer{}}

        // Capture a safe preview of the request body (and restore it for handlers)
        reqCT := r.Header.Get("Content-Type")
        var reqBodyPreview string
        var reqBodySize int
        if shouldLogBody(reqCT) {
            // Read entire body to allow handlers to read it afterwards
            bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, int64(requestReadHardLimit)))
            if err == nil {
                // If body might be larger than hard limit, drain remaining to compute size if Content-Length is known
                if r.ContentLength > 0 {
                    reqBodySize = int(r.ContentLength)
                } else {
                    reqBodySize = len(bodyBytes)
                }
                // Restore the body for the downstream handler
                r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
                reqBodyPreview = previewBytes(bodyBytes, requestLogLimit, reqCT)
            } else {
                reqBodyPreview = "<error reading body>"
                reqBodySize = -1
            }
        } else {
            // Don't consume potentially huge/binary bodies; use Content-Length if available
            if r.ContentLength > 0 {
                reqBodySize = int(r.ContentLength)
            } else {
                reqBodySize = -1
            }
            reqBodyPreview = "<omitted>"
        }

        next.ServeHTTP(lrw, r)

        duration := time.Since(start)

        method := r.Method
        path := r.URL.Path
        query := r.URL.RawQuery
        if query != "" {
            path = path + "?" + query
        }

        ip := clientIP(r)

        status := lrw.statusCode
        if status == 0 {
            status = http.StatusOK
        }
        // Decide whether to log response body content based on content type
        resCT := lrw.Header().Get("Content-Type")
        var resBodyPreview string
        if shouldLogBody(resCT) {
            resBodyPreview = previewBytes(lrw.bodyBuf.Bytes(), responseLogLimit, resCT)
        } else {
            resBodyPreview = "<omitted>"
        }

        // Sizes
        reqSizeStr := sizeString(reqBodySize)
        resSizeStr := sizeString(lrw.bytesWritten)

        log.Printf("%s %s -> %d (%s) in %s from %s\nRequest CT=%q size=%s body=%s\nResponse CT=%q size=%s body=%s",
            method, path, status, resSizeStr, duration, ip,
            reqCT, reqSizeStr, reqBodyPreview,
            resCT, resSizeStr, resBodyPreview,
        )
    })
}

// clientIP tries to read the client IP from common proxy headers, falling back to RemoteAddr.
func clientIP(r *http.Request) string {
    // X-Forwarded-For may contain multiple IPs, take the first
    if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
        parts := strings.Split(xff, ",")
        if len(parts) > 0 {
            return strings.TrimSpace(parts[0])
        }
        return xff
    }
    if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
        return xrip
    }
    return r.RemoteAddr
}

// --- helpers for body logging ---

const (
    // How many bytes of request/response bodies to keep in log preview
    requestLogLimit   = 4096  // 4KB preview for request bodies
    responseLogLimit  = 4096  // 4KB preview for response bodies
    // Hard cap on request body read when we decide to read it fully to restore to handler
    requestReadHardLimit = 2 * 1024 * 1024 // 2MB
)

// shouldLogBody decides whether the content-type is safe to log as text.
func shouldLogBody(contentType string) bool {
    ct := strings.ToLower(contentType)
    switch {
    case strings.Contains(ct, "application/json"),
        strings.Contains(ct, "text/"),
        strings.Contains(ct, "application/xml"),
        strings.Contains(ct, "application/x-www-form-urlencoded"):
        return true
    default:
        return false
    }
}

// previewBytes returns a string preview of b, truncated to limit, attempting to render as UTF-8 safely.
func previewBytes(b []byte, limit int, contentType string) string {
    if len(b) == 0 {
        return "<empty>"
    }
    truncated := ""
    if len(b) > limit {
        b = b[:limit]
        truncated = "… (truncated)"
    }
    // Try to treat as text. If it looks binary, just return a hex-ish placeholder
    if looksTextual(b, contentType) {
        // Replace newlines with \n to keep logs on fewer lines for readability
        s := strings.ReplaceAll(string(b), "\n", "\\n")
        return s + truncated
    }
    return "<binary>" + truncated
}

func looksTextual(b []byte, contentType string) bool {
    ct := strings.ToLower(contentType)
    if strings.Contains(ct, "json") || strings.Contains(ct, "text/") || strings.Contains(ct, "xml") || strings.Contains(ct, "x-www-form-urlencoded") {
        return true
    }
    // Heuristic: check for NUL bytes which often indicate binary
    for _, c := range b {
        if c == 0 {
            return false
        }
    }
    return true
}

func sizeString(n int) string {
    if n < 0 {
        return "unknown"
    }
    return strconv.Itoa(n) + "B"
}
 
