package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crgimenes/empreendedor.dev/config"
	"github.com/crgimenes/empreendedor.dev/lua"
	"github.com/crgimenes/empreendedor.dev/session"
	"github.com/crgimenes/empreendedor.dev/user"
	"github.com/crgimenes/empreendedor.dev/utils"
	"golang.org/x/oauth2"
)

type stateEntry struct {
	Verifier string
	Expires  time.Time
}

var (
	//go:embed assets/index.html
	indexHTML string
	GitTag    = "dev"
	states    = struct {
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
		Authed bool
		User   user.User
	}{Authed: authed, User: u}

	indexTpl := template.Must(template.New("index").Parse(indexHTML))

	err := indexTpl.Execute(w, data)
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

func runLuaFile(name string) {
	// Create a new Lua state.
	L := lua.New()
	defer L.Close()

	L.SetGlobal("BaseURL", config.Cfg.BaseURL)
	L.SetGlobal("Address", config.Cfg.Addrs)
	L.SetGlobal("GitTag", GitTag)
	L.SetGlobal("GitHubClientID", os.Getenv("GITHUB_CLIENT_ID"))
	L.SetGlobal("GitHubClientSecret", os.Getenv("GITHUB_CLIENT_SECRET"))
	L.SetGlobal("XClientID", os.Getenv("X_CLIENT_ID"))
	L.SetGlobal("XClientSecret", os.Getenv("X_CLIENT_SECRET"))

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
	config.Cfg.GitHubClientID = L.MustGetString("GitHubClientID")
	config.Cfg.GitHubClientSecret = L.MustGetString("GitHubClientSecret")
	config.Cfg.XClientID = L.MustGetString("XClientID")
	config.Cfg.XClientSecret = L.MustGetString("XClientSecret")

	if config.Cfg.GitHubClientID == "" ||
		config.Cfg.GitHubClientSecret == "" ||
		config.Cfg.XClientID == "" ||
		config.Cfg.XClientSecret == "" {
		log.Fatal("Missing OAuth2 client ID/secret in environment variables")
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

func gitHubOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     config.Cfg.GitHubClientID,
		ClientSecret: config.Cfg.GitHubClientSecret,
		RedirectURL:  config.Cfg.BaseURL + "/github/oauth/callback",
		Scopes:       []string{"read:user"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://github.com/login/oauth/authorize",
			TokenURL: "https://github.com/login/oauth/access_token",
		},
	}
}

// X (Twitter) OAuth2 config (Authorization Code + PKCE)
func xOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     config.Cfg.XClientID,
		ClientSecret: config.Cfg.XClientSecret,
		RedirectURL:  config.Cfg.BaseURL + "/x/oauth/callback",
		Scopes:       []string{"tweet.read", "users.read"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://twitter.com/i/oauth2/authorize",
			TokenURL: "https://api.twitter.com/2/oauth2/token",
		},
	}
}

func loginGitHubHandler(w http.ResponseWriter, r *http.Request) {
	// Generate state + PKCE; keep both server-side with TTL.
	state := utils.NewOpaqueID()
	verifier, challenge := utils.MakePKCE()
	putState(state, verifier, 10*time.Minute)

	oc := gitHubOAuthConfig()
	authURL := oc.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func loginXHandler(w http.ResponseWriter, r *http.Request) {
	state := utils.NewOpaqueID()
	verifier, challenge := utils.MakePKCE()
	putState(state, verifier, 10*time.Minute)

	oc := xOAuthConfig()
	authURL := oc.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

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

func githubCallbackHandler(w http.ResponseWriter, r *http.Request) {
	recvState := r.URL.Query().Get("state")
	if recvState == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	verifier, ok := takeState(recvState)
	if !ok {
		http.Error(w, "invalid/expired state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	oc := gitHubOAuthConfig()

	tok, err := oc.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	client := oc.Client(ctx, tok)
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "github /user failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		http.Error(w,
			fmt.Sprintf(
				"user endpoint status %d: %s",
				resp.StatusCode,
				string(b)),
			http.StatusBadGateway)
		return
	}

	var gu struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	err = json.NewDecoder(resp.Body).Decode(&gu)
	if err != nil {
		http.Error(w, "decode user failed", http.StatusBadGateway)
		return
	}

	if gu.ID == 0 || gu.Login == "" {
		http.Error(w, "invalid user data", http.StatusBadGateway)
		return
	}

	log.Printf("logged in user: ID=%d, Login=%s, Name=%s, AvatarURL=%s",
		gu.ID, gu.Login, gu.Name, gu.AvatarURL)

	// Create server-side session and set SID cookie
	sid := utils.NewOpaqueID()
	session.Put(sid, user.User{
		ID:        fmt.Sprintf("%d", gu.ID),
		Login:     gu.Login,
		Name:      gu.Name,
		AvatarURL: gu.AvatarURL,
	})
	session.SetCookie(w, sid, 8*time.Hour)

	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

func xCallbackHandler(w http.ResponseWriter, r *http.Request) {
	recvState := r.URL.Query().Get("state")
	if recvState == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	verifier, ok := takeState(recvState)
	if !ok {
		http.Error(w, "invalid/expired state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	oc := xOAuthConfig()

	tok, err := oc.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	client := oc.Client(ctx, tok)
	req, _ := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://api.x.com/2/users/me?user.fields=profile_image_url",
		nil,
	)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "x /2/users/me failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing response body: %v", err)
		}
	}()

	// if API v2 fails with 403, try API v1.1 as fallback
	if resp.StatusCode == http.StatusForbidden {
		log.Printf("API v2 retornou 403, tentando fallback para API v1.1")

		req, _ = http.NewRequestWithContext(ctx, "GET", "https://api.x.com/1.1/account/verify_credentials.json", nil)
		req.Header.Set("Accept", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			http.Error(w, "x verify_credentials failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Printf("Error closing response body: %v", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			http.Error(w,
				fmt.Sprintf("verify_credentials status %d: %s", resp.StatusCode, string(b)),
				http.StatusBadGateway)
			return
		}

		// Parse response from API v1.1 (diferent structure)
		var xuLegacy struct {
			ID              string `json:"id_str"`
			ScreenName      string `json:"screen_name"`
			Name            string `json:"name"`
			ProfileImageURL string `json:"profile_image_url_https"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&xuLegacy); err != nil {
			http.Error(w, "decode user failed", http.StatusBadGateway)
			return
		}

		if xuLegacy.ID == "" || xuLegacy.ScreenName == "" {
			http.Error(w, "invalid user data", http.StatusBadGateway)
			return
		}

		log.Printf("logged in X user (API v1.1): ID=%s, Username=%s, Name=%s, AvatarURL=%s",
			xuLegacy.ID, xuLegacy.ScreenName, xuLegacy.Name, xuLegacy.ProfileImageURL)

		// Create server-side session and set SID cookie usando dados da API v1.1
		sid := utils.NewOpaqueID()
		session.Put(sid, user.User{
			ID:        xuLegacy.ID,
			Login:     xuLegacy.ScreenName,
			Name:      xuLegacy.Name,
			AvatarURL: xuLegacy.ProfileImageURL,
		})
		session.SetCookie(w, sid, 8*time.Hour)
		http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
		return
	}

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		http.Error(w,
			fmt.Sprintf("users/me status %d: %s", resp.StatusCode, string(b)),
			http.StatusBadGateway)
		return
	}

	var xu struct {
		Data struct {
			ID              string `json:"id"`
			Username        string `json:"username"`
			Name            string `json:"name"`
			ProfileImageURL string `json:"profile_image_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&xu); err != nil {
		http.Error(w, "decode user failed", http.StatusBadGateway)
		return
	}

	if xu.Data.ID == "" || xu.Data.Username == "" {
		http.Error(w, "invalid user data", http.StatusBadGateway)
		return
	}

	log.Printf("logged in X user: ID=%s, Username=%s, Name=%s, AvatarURL=%s",
		xu.Data.ID, xu.Data.Username, xu.Data.Name, xu.Data.ProfileImageURL)

	// Create server-side session and set SID cookie (mesmo modelo de usuário)
	sid := utils.NewOpaqueID()
	session.Put(sid, user.User{
		ID:        xu.Data.ID,
		Login:     xu.Data.Username,
		Name:      xu.Data.Name,
		AvatarURL: xu.Data.ProfileImageURL,
	})
	session.SetCookie(w, sid, 8*time.Hour)

	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Llongfile)

	const initLua = "init.lua"

	if !fileExists(initLua) {
		log.Fatal("init.lua not found")
	}

	runLuaFile(initLua)

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/healthz", healthHandler)

	mux.HandleFunc("/login/github", loginGitHubHandler) // GitHub
	mux.HandleFunc("/login/x", loginXHandler)           // X
	mux.HandleFunc("/logout", logoutHandler)
	mux.HandleFunc("/me", meHandler)

	mux.HandleFunc("/github/oauth/callback", githubCallbackHandler)
	mux.HandleFunc("/x/oauth/callback", xCallbackHandler)

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
	signal.Notify(stop, os.Interrupt)
	<-stop

	log.Println("Shutting down gracefully…")
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server stopped.")
}
