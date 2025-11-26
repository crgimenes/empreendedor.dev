package middleware

import (
	"net/http"
	"strings"
)

type RespWriter struct {
	http.ResponseWriter
	status int
}

func NewRespWriter(w http.ResponseWriter) *RespWriter {
	return &RespWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *RespWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *RespWriter) Status() int {
	return w.status
}

func SecurityHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"form-action 'self'",
		"object-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https: *.githubusercontent.com github.com *.twimg.com pbs.twimg.com",
		"frame-ancestors 'none'",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}
