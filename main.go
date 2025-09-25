package main

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crgimenes/empreendedor.dev/assets"
	"github.com/crgimenes/empreendedor.dev/config"
	"github.com/crgimenes/empreendedor.dev/db"
	"github.com/crgimenes/empreendedor.dev/lua"
	"github.com/crgimenes/empreendedor.dev/session"
	"github.com/crgimenes/empreendedor.dev/templates"
	"github.com/crgimenes/empreendedor.dev/user"
)

type stateEntry struct {
	Verifier string
	Expires  time.Time
}

var (
	GitTag = "dev"
	states = struct {
		sync.Mutex
		m map[string]stateEntry
	}{m: make(map[string]stateEntry)}
)

func securityHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"img-src 'self' data: https: *.githubusercontent.com github.com *.twimg.com pbs.twimg.com",
		"style-src 'self' 'unsafe-inline'",
		"frame-ancestors 'none'",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", csp)

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs method, path, status and duration for each request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &respWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		dur := time.Since(start)
		log.Printf("request method=%s path=%s status=%d dur_ms=%s remote=%s", r.Method, r.URL.Path, rw.status, strconv.FormatInt(dur.Milliseconds(), 10), r.RemoteAddr)
	})
}

type respWriter struct {
	http.ResponseWriter
	status int
}

func (w *respWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sid, ok := session.GetCookie(r)
	var u user.User
	authed := false
	if ok {
		if got, ok := session.Get(sid); ok {
			u, authed = got, true
		}
	}
	data := struct {
		Authed           bool
		User             user.User
		FakeOAuthEnabled bool
	}{Authed: authed, User: u, FakeOAuthEnabled: config.Cfg.FakeOAuthEnabled}

	indexTpl, err := template.ParseFS(templates.FS, "index.html")
	if err != nil {
		log.Fatalf("parse template: %v", err)
	}

	err = indexTpl.Execute(w, data)
	if err != nil {
		log.Printf("template execute error: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		panic(err)
	}
	return true
}

func ifEmpty(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func runLuaFile(name string) {
	// Create a new Lua state.
	L := lua.New()
	defer L.Close()

	L.SetGlobal("GitTag", ifEmpty(GitTag, config.Cfg.GitTag))
	L.SetGlobal("BaseURL", ifEmpty(os.Getenv("BASE_URL"), config.Cfg.BaseURL))
	L.SetGlobal("Address", ifEmpty(os.Getenv("ADDRESS"), config.Cfg.Addrs))
	L.SetGlobal("GitHubClientID", os.Getenv("GITHUB_CLIENT_ID"))
	L.SetGlobal("GitHubClientSecret", os.Getenv("GITHUB_CLIENT_SECRET"))
	L.SetGlobal("XClientID", os.Getenv("X_CLIENT_ID"))
	L.SetGlobal("XClientSecret", os.Getenv("X_CLIENT_SECRET"))
	L.SetGlobal("FakeOAuthEnabled", os.Getenv("FAKE_OAUTH_ENABLED") == "true")
	L.SetGlobal("FakeOAuthBaseURL", ifEmpty(
		os.Getenv("FAKE_OAUTH_BASE_URL"), config.Cfg.FakeOAuthBaseURL))
	L.SetGlobal("FakeOAuthClientID", ifEmpty(
		os.Getenv("FAKE_OAUTH_CLIENT_ID"), config.Cfg.FakeOAuthClientID))
	L.SetGlobal("FakeOAuthRedirectPath", ifEmpty(
		os.Getenv("FAKE_OAUTH_REDIRECT_PATH"), config.Cfg.FakeOAuthRedirect))
	L.SetGlobal("DatabaseURL", os.Getenv("DATABASE_URL"))

	// Read the Lua file.
	b, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		log.Fatal(err)
	}

	err = L.DoString(string(b))
	if err != nil {
		log.Fatal(err)
	}

	config.Cfg.Addrs = L.MustGetString("Address")
	config.Cfg.BaseURL = L.MustGetString("BaseURL")
	config.Cfg.DatabaseURL = L.MustGetString("DatabaseURL")
	config.Cfg.FakeOAuthEnabled = L.MustGetBool("FakeOAuthEnabled")
	config.Cfg.GitHubClientID = L.MustGetString("GitHubClientID")
	config.Cfg.GitHubClientSecret = L.MustGetString("GitHubClientSecret")
	config.Cfg.GitTag = L.MustGetString("GitTag")
	config.Cfg.XClientID = L.MustGetString("XClientID")
	config.Cfg.XClientSecret = L.MustGetString("XClientSecret")

	if config.Cfg.FakeOAuthEnabled {

		session.EnableInsecureCookie()

		config.Cfg.FakeOAuthBaseURL = L.MustGetString("FakeOAuthBaseURL")
		config.Cfg.FakeOAuthClientID = L.MustGetString("FakeOAuthClientID")
		config.Cfg.FakeOAuthRedirect = L.MustGetString("FakeOAuthRedirectPath")
	}

	if config.Cfg.DatabaseURL == "" {
		log.Fatal("Missing database URL in configuration")
	}

	// Allow missing real providers if fake OAuth is enabled (for local tests).
	if !config.Cfg.FakeOAuthEnabled {
		if config.Cfg.GitHubClientID == "" ||
			config.Cfg.GitHubClientSecret == "" ||
			config.Cfg.XClientID == "" ||
			config.Cfg.XClientSecret == "" {
			log.Fatal("Missing OAuth2 client ID/secret in configuration")
		}
	}

}

func putState(st, verifier string, ttl time.Duration) {
	states.Lock()
	states.m[st] = stateEntry{Verifier: verifier, Expires: time.Now().Add(ttl)}
	// simple opportunistic cleanup:
	for k, v := range states.m {
		if time.Now().After(v.Expires) {
			delete(states.m, k)
		}
	}
	states.Unlock()
}

func takeState(st string) (string, bool) {
	states.Lock()
	defer func() {
		delete(states.m, st)
		states.Unlock()
	}()
	ent, ok := states.m[st]
	if !ok || time.Now().After(ent.Expires) {
		return "", false
	}
	return ent.Verifier, true
}

// OAuth provider instances (defined in separate files)
var (
	gitHubProvider = GitHubProvider{}
	xProvider      = XProvider{}
	fakeProvider   = FakeProvider{}
)

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if sid, ok := session.GetCookie(r); ok {
		session.Del(sid)
	}
	session.SetCookie(w, "", -1) // clear cookie
	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sid, ok := session.GetCookie(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	u, ok := session.Get(sid)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Llongfile)
	config.Cfg.GitTag = GitTag

	const initLua = "init.lua"

	if !fileExists(initLua) {
		log.Fatal("init.lua not found")
	}

	runLuaFile(initLua)

	var err error

	db.Storage, err = db.New()
	if err != nil {
		log.Fatalf("Error on db: %s", err)
	}

	mux := http.NewServeMux()

	fileServer := http.FileServer(assets.FS)
	mux.Handle("/assets/", http.StripPrefix("/assets/", fileServer))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// some browsers do not support link rel="icon"
		// redirect to the one served from /assets/
		http.Redirect(w, r, "/assets/favicon.ico", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/healthz", healthHandler)

	mux.HandleFunc("/login/github", gitHubProvider.LoginHandler)
	mux.HandleFunc("/login/x", xProvider.LoginHandler)

	if config.Cfg.FakeOAuthEnabled {
		mux.HandleFunc("/login/fake", fakeProvider.LoginHandler)
		mux.HandleFunc(config.Cfg.FakeOAuthRedirect, fakeProvider.CallbackHandler)
	}
	mux.HandleFunc("/logout", logoutHandler)
	mux.HandleFunc("/me", meHandler)

	mux.HandleFunc("/github/oauth/callback", gitHubProvider.CallbackHandler)
	mux.HandleFunc("/x/oauth/callback", xProvider.CallbackHandler)

	srv := &http.Server{
		Addr:              config.Cfg.Addrs,
		Handler:           loggingMiddleware(securityHeaders(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start server in a goroutine to enable graceful shutdown below.
	go func() {
		log.Printf("Serving on %s", config.Cfg.Addrs)
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe error: %v", err)
		}
	}()

	// session Cleanup
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			session.Cleanup()
		}
	}()

	// Graceful shutdown on Ctrl+C (SIGINT).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	log.Println("Shutting down gracefully…")
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second)
	defer cancel()

	err = srv.Shutdown(ctx)
	if err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server stopped.")
}
