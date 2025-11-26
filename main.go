package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	static "edev/assets/static"
	"edev/auth"
	"edev/config"
	"edev/db"
	"edev/filemanager"
	"edev/forum"
	"edev/handlers"
	"edev/log"
	"edev/lua"
	"edev/middleware"
	"edev/session"
	"edev/templates"
)

type stateEntry struct {
	Verifier string
	Expires  time.Time
}

var (
	GitTag = "dev" + time.Now().UTC().Format("-20060102-150405")
	states = struct {
		sync.Mutex
		m map[string]stateEntry
	}{m: make(map[string]stateEntry)}
)

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
	L.SetGlobal("BaseURL", ifEmpty(os.Getenv("EDEV_BASE_URL"), config.Cfg.BaseURL))
	L.SetGlobal("Address", ifEmpty(os.Getenv("EDEV_ADDRESS"), config.Cfg.Addrs))

	L.SetGlobal("DiscordOAuthEnabled", os.Getenv("EDEV_DISCORD_OAUTH_ENABLED") == "true")
	L.SetGlobal("DiscordClientID", os.Getenv("EDEV_DISCORD_CLIENT_ID"))
	L.SetGlobal("DiscordClientSecret", os.Getenv("EDEV_DISCORD_CLIENT_SECRET"))

	L.SetGlobal("GithubOAuthEnabled", os.Getenv("EDEV_GITHUB_OAUTH_ENABLED") == "true")
	L.SetGlobal("GitHubClientID", os.Getenv("EDEV_GITHUB_CLIENT_ID"))
	L.SetGlobal("GitHubClientSecret", os.Getenv("EDEV_GITHUB_CLIENT_SECRET"))

	L.SetGlobal("XOAuthEnabled", os.Getenv("EDEV_X_OAUTH_ENABLED") == "true")
	L.SetGlobal("XClientID", os.Getenv("EDEV_X_CLIENT_ID"))
	L.SetGlobal("XClientSecret", os.Getenv("EDEV_X_CLIENT_SECRET"))

	L.SetGlobal("FakeOAuthEnabled", os.Getenv("EDEV_FAKE_OAUTH_ENABLED") == "true")
	L.SetGlobal("FakeOAuthBaseURL", ifEmpty(
		os.Getenv("EDEV_FAKE_OAUTH_BASE_URL"), config.Cfg.FakeOAuthBaseURL))
	L.SetGlobal("FakeOAuthClientID", ifEmpty(
		os.Getenv("EDEV_FAKE_OAUTH_CLIENT_ID"), config.Cfg.FakeOAuthClientID))
	L.SetGlobal("FakeOAuthRedirectPath", ifEmpty(
		os.Getenv("EDEV_FAKE_OAUTH_REDIRECT_PATH"), config.Cfg.FakeOAuthRedirect))

	L.SetGlobal("DBFile", ifEmpty(
		os.Getenv("EDEV_DB_FILE"), config.Cfg.DBFile))

	L.SetGlobal("ResendAPIKey", ifEmpty(
		os.Getenv("EDEV_RESEND_API_KEY"), config.Cfg.ResendAPIKey))

	L.SetGlobal("EmailDomain", ifEmpty(
		os.Getenv("EDEV_EMAIL_DOMAIN"), config.Cfg.EmailDomain))

	L.SetGlobal("SiteTitle", ifEmpty(
		os.Getenv("EDEV_SITE_TITLE"), config.Cfg.SiteTitle))
	L.SetGlobal("SiteDescription", ifEmpty(
		os.Getenv("EDEV_SITE_DESCRIPTION"), config.Cfg.SiteDescription))

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
	config.Cfg.FakeOAuthEnabled = L.MustGetBool("FakeOAuthEnabled")

	config.Cfg.DiscordOAuthEnabled = L.MustGetBool("DiscordOAuthEnabled")
	config.Cfg.DiscordClientID = L.MustGetString("DiscordClientID")
	config.Cfg.DiscordClientSecret = L.MustGetString("DiscordClientSecret")

	config.Cfg.GithubOAuthEnabled = L.MustGetBool("GithubOAuthEnabled")
	config.Cfg.GitHubClientID = L.MustGetString("GitHubClientID")
	config.Cfg.GitHubClientSecret = L.MustGetString("GitHubClientSecret")
	config.Cfg.GitTag = L.MustGetString("GitTag")

	config.Cfg.XOAuthEnabled = L.MustGetBool("XOAuthEnabled")
	config.Cfg.XClientID = L.MustGetString("XClientID")
	config.Cfg.XClientSecret = L.MustGetString("XClientSecret")
	config.Cfg.DBFile = L.MustGetString("DBFile")

	if config.Cfg.FakeOAuthEnabled {

		session.EnableInsecureCookie()

		config.Cfg.FakeOAuthBaseURL = L.MustGetString("FakeOAuthBaseURL")
		config.Cfg.FakeOAuthClientID = L.MustGetString("FakeOAuthClientID")
		config.Cfg.FakeOAuthRedirect = L.MustGetString("FakeOAuthRedirectPath")
	}

	config.Cfg.ResendAPIKey = L.MustGetString("ResendAPIKey")
	config.Cfg.EmailDomain = L.MustGetString("EmailDomain")

	if config.Cfg.BaseURL == "http://localhost:3210" ||
		strings.HasPrefix(config.Cfg.BaseURL, "http://callisto:3210") {
		session.EnableInsecureCookie()
	}

	config.Cfg.SiteTitle = L.MustGetString("SiteTitle")
	config.Cfg.SiteDescription = L.MustGetString("SiteDescription")

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
	gitHubProvider  = GitHubProvider{}
	discordProvider = DiscordProvider{}
	xProvider       = XProvider{}
	fakeProvider    = FakeProvider{}
)

func sseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// check session - session is required for SSE
	sid, ok := session.GetCookie(r)
	if !ok {
		log.Printf("SSE: No session cookie found")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	u, ok := session.Get(sid)
	if !ok {
		log.Printf("SSE: Session %s not found in store", sid[:min(8, len(sid))])
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return // FIXED: was missing return statement
	}

	// user must be enabled to use SSE
	if !u.Enabled {
		log.Printf("SSE: User %s is not enabled (enabled=%v)", u.Email, u.Enabled)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Disable write deadline for SSE connections
	rc := http.NewResponseController(w)
	if rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	// Set headers for SSE - critical for client reconnection behavior
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Keep-Alive", "timeout=60")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	// Ensure response writer supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send initial SSE directives/data to confirm connection on client
	// Optional retry directive (client default is fine, but explicit is ok)
	_, _ = fmt.Fprintf(w, "retry: 10000\n") // suggest 10s retry if client reconnects
	_, _ = fmt.Fprintf(w, "data: ready\n\n")
	flusher.Flush()

	// Create channel for this session with a larger buffer to absorb short bursts
	ch := make(chan string, 64)
	session.RegisterSSEChannel(sid, ch)

	defer func() {
		session.UnregisterSSEChannel(sid, ch)
		close(ch)
	}()

	ctx := r.Context()
	// Many proxies use a 30s idle timeout; send heartbeat sooner to avoid drop
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// periodic heartbeat to keep connection alive (prevents timeout by proxy/firewall)
	// and allows detecting client disconnection
	for {
		select {
		case <-ctx.Done():
			// Client disconnected or connection timeout
			//log.Printf("SSE context done for user %s: %v", u.Email, ctx.Err())
			return

		case <-ticker.C:
			// Send heartbeat as data event so clients see activity and proxies keep stream
			_, err := fmt.Fprintf(w, "data: heartbeat\n\n")
			if err != nil {
				log.Printf("SSE heartbeat write error: %v", err)
				return
			}
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				// Channel closed, connection shutting down
				return
			}

			// Format message according to SSE spec
			// Format: event: type\ndata: payload\n\n
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
			if err != nil {
				log.Printf("SSE write error: %v", err)
				return
			}
			flusher.Flush()
			log.Printf("SSE message sent to user %s: %q", u.Email, msg)
		}
	}
}

func main() {
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

	err = db.RunMigration()
	if err != nil {
		log.Fatalf("Migration error: %v", err)
	}

	go func() {
		for {
			time.Sleep(1 * time.Hour)
			err := db.Storage.PurgeExpiredMagicLinkTokens()
			if err != nil {
				log.Printf("Error purging expired magic link tokens: %v", err)
			}
		}
	}()

	// Load sessions from file
	err = session.LoadFromGobFile("sessions.gob")
	if err != nil {
		log.Printf("Session load error: %v", err)
		return
	}

	err = static.Init()
	if err != nil {
		log.Fatalf("Static assets init error: %v", err)
	}

	h := handlers.New(handlers.Dependencies{
		Config:    config.Cfg,
		Templates: templates.ExecuteTemplate,
		FileUtilities: handlers.FileUtilities{
			Validate:     filemanager.ValidateFile,
			DataPath:     filemanager.DataFilePath,
			SaveMetadata: filemanager.SaveFileMetadata,
			NewFilename:  filemanager.FileName,
		},
	})

	mux := http.NewServeMux()

	mux.HandleFunc("/assets/", static.Handler)

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// redirect to the one served from /assets/
		http.Redirect(w, r, "/assets/favicon.ico", http.StatusMovedPermanently)
	})

	mux.HandleFunc("/", h.Home)
	mux.HandleFunc("/login", h.LoginPage)
	mux.HandleFunc("POST /login/magic_link", h.LoginMagic) // for email link login and magic link

	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("GET /link/{token}", h.MagicLink) // for email link login and magic link

	if config.Cfg.DiscordOAuthEnabled {
		mux.HandleFunc("/login/discord", discordProvider.LoginHandler)
		mux.HandleFunc("/discord/oauth/callback", discordProvider.CallbackHandler)
	}

	if config.Cfg.GithubOAuthEnabled {
		mux.HandleFunc("/login/github", gitHubProvider.LoginHandler)
		mux.HandleFunc("/github/oauth/callback", gitHubProvider.CallbackHandler)
	}

	if config.Cfg.XOAuthEnabled {
		mux.HandleFunc("/login/x", xProvider.LoginHandler)
		mux.HandleFunc("/x/oauth/callback", xProvider.CallbackHandler)
	}

	if config.Cfg.FakeOAuthEnabled {
		mux.HandleFunc("/login/fake", fakeProvider.LoginHandler)
		mux.HandleFunc(config.Cfg.FakeOAuthRedirect, fakeProvider.CallbackHandler)
	}

	mux.HandleFunc("/events", sseHandler) // server-sent events)
	mux.HandleFunc("/logout", auth.Logout)
	mux.HandleFunc("/me", h.Profile) // user profile

	// Debug broadcast endpoint (unsafe, for manual testing only)
	mux.HandleFunc("/msg", func(w http.ResponseWriter, r *http.Request) {
		msg := r.URL.Query().Get("msg")
		if msg == "" {
			msg = "debug"
		}
		n := session.BroadcastSSENotification(msg)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "sent to %d channels\n", n)
	})

	// filemanager routes (user files, images, etc.)
	filemanager.Routes(mux)

	// Forum routes - inject dependencies
	forum.SetMdToHTML(mdToHTML)
	forum.Routes(mux)

	// ------------------------------------------
	APIRoutes(mux)

	// ------------------------------------------
	srv := &http.Server{
		Addr:              config.Cfg.Addrs,
		Handler:           middleware.SecurityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0, // ***CRITICAL*** disable write timeout for long-lived connections (SSE)
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
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down gracefully (no context timeout)...")
	// Disable keep-alives to encourage clients to disconnect quickly.
	srv.SetKeepAlivesEnabled(false)

	// Notify SSE clients (best-effort); they will see disconnect soon after.
	n := session.BroadcastSSENotification("shutdown")
	log.Printf("Broadcasted shutdown to %d SSE channels", n)
	// Small pause to allow kernel buffers to flush messages.
	time.Sleep(250 * time.Millisecond)

	// Save sessions to file
	err = session.SaveToGobFile("sessions.gob")
	if err != nil {
		log.Printf("Session save error: %v", err)
	}

	// Direct close without waiting for a context deadline.
	if cerr := srv.Close(); cerr != nil {
		log.Printf("Server close error: %v", cerr)
	}

	if db.Storage != nil {
		db.Storage.Close()
	}
	log.Println("Server stopped.")
}
