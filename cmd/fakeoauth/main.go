package main

// fakeoauth: Servidor OAuth2 de teste (Authorization Code + PKCE) somente para DEV/TEST.
// Nao usar em producao. Sem HTTPS, sem UI de consentimento, autoriza sempre o usuario fixo.
// Exemplos:
//  go run ./cmd/fakeoauth
//  fakeoauth --addr 127.0.0.1:9100 --base-url http://127.0.0.1:9100 \
//    --client-id fake-client-id --user-id u-123 --username tester \
//    --name "Test User" --email tester@example.local
//
//  fakeoauth --issue-id-token --jwt-secret dev-secret
//
// Fluxo tipico (shell):
//  AUTHZ_URL="http://127.0.0.1:9100/oauth/authorize?response_type=code&client_id=fake-client-id&redirect_uri=http://127.0.0.1:8080/fake/oauth/callback&scope=profile+email&state=abc&code_challenge=xyz&code_challenge_method=S256"
//  curl -v "$AUTHZ_URL" -L
//  # Recebera redirecionamento com ?code=...&state=abc
//  curl -X POST http://127.0.0.1:9100/oauth/token \
//    -d grant_type=authorization_code \
//    -d code=CODE \
//    -d redirect_uri=http://127.0.0.1:8080/fake/oauth/callback \
//    -d client_id=fake-client-id \
//    -d code_verifier=ORIGINAL_VERIFIER
//
import (
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	mrand "math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"edev/log"
)

type authCode struct {
	UserID        string
	RedirectURI   string
	ExpiresAt     time.Time
	CodeChallenge string // opcional
	Scope         string
}

type accessToken struct {
	UserID    string
	Username  string
	Name      string
	Email     string
	AvatarURL string
	ExpiresAt time.Time
	Scope     string
}

type store struct {
	sync.Mutex
	codes  map[string]authCode
	tokens map[string]accessToken
}

func newStore() *store {
	return &store{codes: make(map[string]authCode), tokens: make(map[string]accessToken)}
}

func (s *store) putCode(c string, ac authCode) { s.Lock(); s.codes[c] = ac; s.Unlock() }
func (s *store) takeCode(c string) (authCode, bool) {
	s.Lock()
	ac, ok := s.codes[c]
	if ok {
		delete(s.codes, c)
	}
	s.Unlock()
	return ac, ok
}
func (s *store) putToken(t string, at accessToken) { s.Lock(); s.tokens[t] = at; s.Unlock() }
func (s *store) getToken(t string) (accessToken, bool) {
	s.Lock()
	at, ok := s.tokens[t]
	if ok && time.Now().After(at.ExpiresAt) {
		delete(s.tokens, t)
		ok = false
	}
	s.Unlock()
	return at, ok
}
func (s *store) cleanupExpired() {
	s.Lock()
	now := time.Now()
	for k, v := range s.codes {
		if now.After(v.ExpiresAt) {
			delete(s.codes, k)
		}
	}
	for k, v := range s.tokens {
		if now.After(v.ExpiresAt) {
			delete(s.tokens, k)
		}
	}
	s.Unlock()
}

// randomString gera identificadores opacos base64url (sem padding) de n bytes de entropia.
func randomString(n int) string {
	b := make([]byte, n)
	_, err := crand.Read(b)
	if err != nil { // fallback improvavel
		mrand.Seed(time.Now().UnixNano())
		for i := range b {
			b[i] = byte(mrand.Intn(256))
		}
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func base64urlSHA256(in string) string {
	h := sha256.Sum256([]byte(in))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// jwtHS256 minimalista para id_token (apenas DEV/TEST) – nao suportar header extra.
func jwtHS256(secret string, claims map[string]any) (string, error) {
	head := map[string]string{"alg": "HS256", "typ": "JWT"}
	hb, err := json.Marshal(head)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	unsigned := enc(hb) + "." + enc(cb)
	hm := hmac.New(sha256.New, []byte(secret))
	_, _ = hm.Write([]byte(unsigned))
	sig := enc(hm.Sum(nil))
	return unsigned + "." + sig, nil
}

type config struct {
	Addr         string
	BaseURL      string
	ClientID     string
	ClientSecret string
	UserID       string
	Username     string
	Name         string
	Email        string
	AvatarURL    string
	IssueIDToken bool
	JWTSecret    string
	TokenTTL     time.Duration
	Latency      time.Duration
	Verbose      bool
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.Addr, "addr", "127.0.0.1:9100", "listen address")
	flag.StringVar(&cfg.BaseURL, "base-url", "http://127.0.0.1:9100", "public base URL")
	flag.StringVar(&cfg.ClientID, "client-id", "fake-client-id", "expected client_id")
	flag.StringVar(&cfg.ClientSecret, "client-secret", "", "expected client_secret (optional)")
	flag.StringVar(&cfg.UserID, "user-id", "u-123", "fixed user id")
	flag.StringVar(&cfg.Username, "username", "tester", "username/login")
	flag.StringVar(&cfg.Name, "name", "Test User", "user display name")
	flag.StringVar(&cfg.Email, "email", "tester@example.local", "user email")
	flag.StringVar(&cfg.AvatarURL, "avatar-url", "", "avatar URL (optional)")
	flag.BoolVar(&cfg.IssueIDToken, "issue-id-token", false, "issue id_token (JWT HS256)")
	flag.StringVar(&cfg.JWTSecret, "jwt-secret", "dev-secret", "JWT HMAC secret")
	flag.DurationVar(&cfg.TokenTTL, "token-ttl", 15*time.Minute, "access token TTL")
	flag.DurationVar(&cfg.Latency, "latency", 0, "artificial latency for all endpoints")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "verbose logging")
	flag.Parse()
	return cfg
}

func errorJSON(w http.ResponseWriter, code int, errCode, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": desc,
	})
}

func authorizeHandler(cfg config, st *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Latency > 0 {
			time.Sleep(cfg.Latency)
		}
		q := r.URL.Query()
		if q.Get("response_type") != "code" {
			errorJSON(w, 400, "unsupported_response_type", "expected response_type=code")
			return
		}
		clientID := q.Get("client_id")
		if clientID != cfg.ClientID {
			errorJSON(w, 400, "unauthorized_client", "invalid client_id")
			return
		}
		redirectURI := q.Get("redirect_uri")
		if redirectURI == "" || !(strings.HasPrefix(redirectURI, "http://") || strings.HasPrefix(redirectURI, "https://")) {
			errorJSON(w, 400, "invalid_request", "invalid redirect_uri")
			return
		}
		codeChallenge := q.Get("code_challenge")
		codeChallengeMethod := q.Get("code_challenge_method")
		if codeChallengeMethod != "" && codeChallengeMethod != "S256" {
			errorJSON(w, 400, "invalid_request", "only S256 supported for code_challenge_method")
			return
		}
		ac := authCode{
			UserID:        cfg.UserID,
			RedirectURI:   redirectURI,
			ExpiresAt:     time.Now().Add(2 * time.Minute),
			CodeChallenge: "",
			Scope:         q.Get("scope"),
		}
		if codeChallenge != "" && codeChallengeMethod == "S256" {
			ac.CodeChallenge = codeChallenge
		}
		code := randomString(24)
		st.putCode(code, ac)
		v := url.Values{}
		v.Set("code", code)
		if state := q.Get("state"); state != "" {
			v.Set("state", state)
		}
		redir, _ := url.Parse(redirectURI)
		qs := redir.Query()
		for k, vals := range v {
			for _, val := range vals {
				qs.Set(k, val)
			}
		}
		redir.RawQuery = qs.Encode()
		if cfg.Verbose {
			log.Printf("authorize: issued code=%s state=%s", code, q.Get("state"))
		}
		http.Redirect(w, r, redir.String(), http.StatusFound)
	}
}

func tokenHandler(cfg config, st *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Latency > 0 {
			time.Sleep(cfg.Latency)
		}
		if err := r.ParseForm(); err != nil {
			errorJSON(w, 400, "invalid_request", "parse form")
			return
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			errorJSON(w, 400, "unsupported_grant_type", "expected authorization_code")
			return
		}
		code := r.PostForm.Get("code")
		redirectURI := r.PostForm.Get("redirect_uri")
		clientID := r.PostForm.Get("client_id")
		if clientID != cfg.ClientID {
			errorJSON(w, 400, "unauthorized_client", "invalid client_id")
			return
		}
		ac, ok := st.takeCode(code)
		if !ok {
			errorJSON(w, 400, "invalid_grant", "unknown code")
			return
		}
		if time.Now().After(ac.ExpiresAt) {
			errorJSON(w, 400, "invalid_grant", "expired code")
			return
		}
		if ac.RedirectURI != redirectURI {
			errorJSON(w, 400, "invalid_grant", "redirect_uri mismatch")
			return
		}
		if ac.CodeChallenge != "" { // PKCE S256
			verifier := r.PostForm.Get("code_verifier")
			if verifier == "" {
				errorJSON(w, 400, "invalid_request", "missing code_verifier")
				return
			}
			if base64urlSHA256(verifier) != ac.CodeChallenge {
				errorJSON(w, 400, "invalid_grant", "code_verifier mismatch")
				return
			}
		}
		accessTok := randomString(32)
		at := accessToken{
			UserID:    cfg.UserID,
			Username:  cfg.Username,
			Name:      cfg.Name,
			Email:     cfg.Email,
			AvatarURL: cfg.AvatarURL,
			ExpiresAt: time.Now().Add(cfg.TokenTTL),
			Scope:     ac.Scope,
		}
		st.putToken(accessTok, at)
		resp := map[string]any{
			"access_token":  accessTok,
			"token_type":    "Bearer",
			"expires_in":    int(cfg.TokenTTL.Seconds()),
			"refresh_token": "refresh-" + randomString(12),
		}
		if at.Scope != "" {
			resp["scope"] = at.Scope
		}
		if cfg.IssueIDToken {
			claims := map[string]any{
				"iss":                cfg.BaseURL,
				"aud":                cfg.ClientID,
				"sub":                cfg.UserID,
				"exp":                time.Now().Add(cfg.TokenTTL).Unix(),
				"iat":                time.Now().Unix(),
				"email":              cfg.Email,
				"name":               cfg.Name,
				"preferred_username": cfg.Username,
			}
			if cfg.AvatarURL != "" {
				claims["picture"] = cfg.AvatarURL
			}
			jwt, err := jwtHS256(cfg.JWTSecret, claims)
			if err != nil {
				errorJSON(w, 500, "server_error", "jwt generation failed")
				return
			}
			resp["id_token"] = jwt
		}
		w.Header().Set("Content-Type", "application/json")
		if cfg.Verbose {
			log.Printf("token: issued access_token for user=%s", cfg.UserID)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func userInfoHandler(cfg config, st *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Latency > 0 {
			time.Sleep(cfg.Latency)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			errorJSON(w, 401, "invalid_token", "missing bearer")
			return
		}
		tok := strings.TrimPrefix(auth, "Bearer ")
		at, ok := st.getToken(tok)
		if !ok {
			errorJSON(w, 401, "invalid_token", "unknown or expired token")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         at.UserID,
			"username":   at.Username,
			"name":       at.Name,
			"email":      at.Email,
			"avatar_url": at.AvatarURL,
		})
	}
}

func janitor(st *store) {
	for {
		time.Sleep(30 * time.Second)
		st.cleanupExpired()
	}
}

func main() {
	cfg := parseFlags()
	if cfg.Addr == "" {
		log.Fatal("addr required")
	}
	if cfg.BaseURL == "" {
		log.Fatal("base-url required")
	}
	st := newStore()
	go janitor(st)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/authorize", authorizeHandler(cfg, st))
	mux.HandleFunc("/oauth/token", tokenHandler(cfg, st))
	mux.HandleFunc("/oauth/userinfo", userInfoHandler(cfg, st))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })

	server := &http.Server{Addr: cfg.Addr, Handler: loggingMiddleware(cfg, mux)}
	log.Printf("fakeoauth listening on %s (client_id=%s)", cfg.Addr, cfg.ClientID)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func loggingMiddleware(cfg config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if cfg.Verbose {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
		}
	})
}
