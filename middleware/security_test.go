package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	SecurityHeaders(handler).ServeHTTP(recorder, req)

	tests := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Strict-Transport-Security":    "max-age=63072000; includeSubDomains; preload",
		"Cross-Origin-Resource-Policy": "same-origin",
	}

	for key, want := range tests {
		got := recorder.Header().Get(key)
		if got != want {
			t.Fatalf("header %s: expected %q got %q", key, want, got)
		}
	}

	wantedCSP := strings.Join([]string{
		"default-src 'self'",
		"form-action 'self'",
		"object-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https: *.githubusercontent.com github.com *.twimg.com pbs.twimg.com",
		"frame-ancestors 'none'",
	}, "; ")

	gotCSP := recorder.Header().Get("Content-Security-Policy")
	if gotCSP != wantedCSP {
		t.Fatalf("Content-Security-Policy: expected %q got %q", wantedCSP, gotCSP)
	}
}

func TestRespWriterStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := NewRespWriter(recorder)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(writer, req)

	if writer.Status() != http.StatusTeapot {
		t.Fatalf("status: expected %d got %d", http.StatusTeapot, writer.Status())
	}

	if recorder.Code != http.StatusTeapot {
		t.Fatalf("response recorder: expected %d got %d", http.StatusTeapot, recorder.Code)
	}
}
