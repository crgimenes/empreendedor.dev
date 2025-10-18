package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"edev/assets"
	"edev/config"
	"edev/db"
	"edev/log"
	"edev/lua"
	"edev/mail"
	"edev/migration"
	"edev/session"
	"edev/templates"
	"edev/user"
	"edev/utils"
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

	// Check for message in query parameter
	message := r.URL.Query().Get("message")

	data := struct {
		Authed  bool
		User    user.User
		Error   string
		Message string
		Config  config.Config
	}{
		Authed:  authed,
		User:    u,
		Message: message,
		Config:  *config.Cfg,
	}

	err := templates.ExecuteTemplate(w, "index.go.tmpl", data)
	if err != nil {
		log.Printf("template %s execute error: %v", "index.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func loginPageHandler(w http.ResponseWriter, r *http.Request) {
	// If already authenticated, redirect to home
	if sid, ok := session.GetCookie(r); ok {
		if _, ok := session.Get(sid); ok {
			http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
			return
		}
	}

	sid, ok := session.GetCookie(r)
	var u user.User
	authed := false
	if ok {
		if got, ok := session.Get(sid); ok {
			u, authed = got, true
		}
	}

	data := struct {
		Authed  bool
		User    user.User
		Error   string
		Message string
		Config  config.Config
	}{
		Authed: authed,
		User:   u,
		Config: *config.Cfg,
	}

	err := templates.ExecuteTemplate(w, "login.go.tmpl", data)
	if err != nil {
		log.Printf("template %s execute error: %v", "login.go.tmpl", err)
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

	L.SetGlobal("GithubOAuthEnabled", os.Getenv("GITHUB_OAUTH_ENABLED") == "true")
	L.SetGlobal("GitHubClientID", os.Getenv("GITHUB_CLIENT_ID"))
	L.SetGlobal("GitHubClientSecret", os.Getenv("GITHUB_CLIENT_SECRET"))

	L.SetGlobal("XOAuthEnabled", os.Getenv("X_OAUTH_ENABLED") == "true")
	L.SetGlobal("XClientID", os.Getenv("X_CLIENT_ID"))
	L.SetGlobal("XClientSecret", os.Getenv("X_CLIENT_SECRET"))

	L.SetGlobal("FakeOAuthEnabled", os.Getenv("FAKE_OAUTH_ENABLED") == "true")
	L.SetGlobal("FakeOAuthBaseURL", ifEmpty(
		os.Getenv("FAKE_OAUTH_BASE_URL"), config.Cfg.FakeOAuthBaseURL))
	L.SetGlobal("FakeOAuthClientID", ifEmpty(
		os.Getenv("FAKE_OAUTH_CLIENT_ID"), config.Cfg.FakeOAuthClientID))
	L.SetGlobal("FakeOAuthRedirectPath", ifEmpty(
		os.Getenv("FAKE_OAUTH_REDIRECT_PATH"), config.Cfg.FakeOAuthRedirect))

	L.SetGlobal("DBFile", ifEmpty(
		os.Getenv("DB_FILE"), config.Cfg.DBFile))

	L.SetGlobal("ResendAPIKey", ifEmpty(
		os.Getenv("RESEND_API_KEY"), config.Cfg.ResendAPIKey))

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
		http.Redirect(w, r, config.Cfg.BaseURL+"/login", http.StatusFound)
		return
	}

	u, ok := session.Get(sid)
	if !ok {
		http.Redirect(w, r, config.Cfg.BaseURL+"/login", http.StatusFound)
		return
	}

	if r.Method == "GET" {
		// Show profile form
		data := struct {
			Authed  bool
			User    user.User
			Error   string
			Message string
			Config  config.Config
		}{
			Authed: true,
			User:   u,
			Config: *config.Cfg,
		}
		err := templates.ExecuteTemplate(w, "me.go.tmpl", data)
		if err != nil {
			log.Printf("template %s execute error: %v", "me.go.tmpl", err)
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	if r.Method == "POST" {
		// Process profile update
		err := r.ParseForm()
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		username := r.FormValue("username")
		avatarURL := r.FormValue("avatar_url")

		// Update user profile
		updatedUser, err := db.Storage.UpdateUserProfile(u.ID, username, avatarURL)
		if err != nil {
			log.Printf("error updating user profile: %v", err)
			// Re-render form with error
			data := struct {
				Authed  bool
				User    user.User
				Error   string
				Message string
				Config  config.Config
			}{
				Authed: true,
				User:   u,
				Error:  err.Error(),
				Config: *config.Cfg,
			}
			err2 := templates.ExecuteTemplate(w, "me.go.tmpl", data)
			if err2 != nil {
				log.Printf("template %s execute error: %v", "me.go.tmpl", err2)
			}
			return
		}

		// Update session with new user data
		session.Put(sid, *updatedUser)

		log.Printf("user %s updated profile", updatedUser.Email)

		// Redirect to home
		http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handlerLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token := r.PathValue("token")

	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	email, err := db.Storage.ConsumeMagicLinkToken(token)
	if err != nil {
		log.Printf("error consuming magic link token: %v", err)
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}
	if email == "" {
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}

	u, err := db.Storage.GetUserOrCreateByEmail(email)
	if err != nil {
		log.Printf("error getting or creating user by email: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, config.Cfg.SessionDuration)

	log.Printf("user %s logged in via magic link", u.Email)

	if u.Username == "" {
		log.Printf("user %s has no username, redirecting to /me", u.Email)
		// redirect to complete profile
		http.Redirect(w, r, config.Cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	// redirect to home
	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)

}

func handlerLoginMagic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}

	email, err = mail.CanonicalizeEmail(email)
	if err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	// generate a random token
	token := utils.RandomString(16)

	// store the token with the email and expiration (15 minutes)
	err = db.Storage.StoreMagicLinkToken(token, email, time.Now().UTC().Add(15*time.Minute))
	if err != nil {
		log.Printf("error storing magic link token: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// send the email with the link
	link := config.Cfg.BaseURL + "/link/" + token
	ret, err := mail.Send(mail.EmailRequest{
		From:    "noreply@" + strings.TrimPrefix(config.Cfg.BaseURL, "https://"),
		To:      []string{email},
		Subject: "Seu link de acesso magico",
		Text: "Clique no link para fazer login: " +
			link +
			"\nEste link expira em 15 minutos.\n--\nEdev",
	})
	if err != nil {
		log.Printf("error sending magic link email: %v", err)
		// do not reveal the error to the user
	}

	log.Printf("sent magic link email to %s, id=%s", email, ret)

	// Return redirect URL
	redirectURL := config.Cfg.BaseURL + "/?message=" + url.QueryEscape("Link de acesso enviado! Verifique seu email.")
	http.Redirect(w, r, redirectURL, http.StatusFound)

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

	err = migration.Run()
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

	mux := http.NewServeMux()

	fileServer := http.FileServer(assets.FS)
	mux.Handle("/assets/", http.StripPrefix("/assets/", fileServer))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// some browsers do not support link rel="icon"
		// redirect to the one served from /assets/
		http.Redirect(w, r, "/assets/favicon.ico", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/login", loginPageHandler)
	mux.HandleFunc("POST /login/magic_link", handlerLoginMagic) // for email link login and magic link

	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("GET /link/{token}", handlerLink) // for email link login and magic link

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

	mux.HandleFunc("/logout", logoutHandler)
	mux.HandleFunc("/me", meHandler)

	srv := &http.Server{
		Addr:              config.Cfg.Addrs,
		Handler:           securityHeaders(mux),
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
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	if db.Storage != nil {
		db.Storage.Close()
	}
	log.Println("Server stopped.")
}
