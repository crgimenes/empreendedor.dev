package main

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

type Health struct {
	Status string `json:"status"`
	Now    string `json:"now"`
}

func apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(true)
		_ = enc.Encode(Health{Status: "ok", Now: time.Now().Format(time.RFC3339Nano)})
	})
	// Handlers REST/HTML reais entram aqui...
	return mux
}

func serveTCP(addr string, h http.Handler) *http.Server {
	s := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second, // mantém keep-alive
	}
	go func() {
		log.Printf("TCP listening on %s", addr)
		if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("tcp server error: %v", err)
		}
	}()
	return s
}

func serveUDS(path string, h http.Handler) (*http.Server, net.Listener, error) {
	_ = os.Remove(path) // remove stale socket
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, err
	}
	// permissões do socket (só dono/leitura-escrita)
	_ = os.Chmod(path, 0o660)

	s := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("UDS listening on %s", path)
		if err := s.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("uds server error: %v", err)
		}
	}()
	return s, ln, nil
}

func main() {
	mux := apiMux()

	// Externo (ex.: por trás do Caddy com TLS/H2/H3)
	_ = serveTCP(":8080", mux)

	// Interno (cliente HTML/renderer no mesmo host)
	_, _, err := serveUDS("/tmp/app_api.sock", mux)
	if err != nil {
		log.Fatal(err)
	}

	select {} // bloqueia
}
