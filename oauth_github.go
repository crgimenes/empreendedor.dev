package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"edev/config"
	"edev/db"
	"edev/log"
	"edev/session"
	"edev/utils"

	"golang.org/x/oauth2"
)

type OAuthProvider interface {
	LoginHandler(w http.ResponseWriter, r *http.Request)
	CallbackHandler(w http.ResponseWriter, r *http.Request)
}

type GitHubProvider struct{}

func (GitHubProvider) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     config.Cfg.GitHubClientID,
		ClientSecret: config.Cfg.GitHubClientSecret,
		RedirectURL:  config.Cfg.BaseURL + "/github/oauth/callback",
		Scopes:       []string{"read:user", "user:email"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://github.com/login/oauth/authorize",
			TokenURL: "https://github.com/login/oauth/access_token",
		},
	}
}

func (p GitHubProvider) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state := utils.NewOpaqueID()
	verifier, challenge := utils.MakePKCE()
	putState(state, verifier, 10*time.Minute)

	oc := p.config()
	authURL := oc.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (p GitHubProvider) CallbackHandler(w http.ResponseWriter, r *http.Request) {
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
	oc := p.config()

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		http.Error(w, "read github user failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("github user response: %s", string(body))

	var gu struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		Email     string `json:"email"`
	}
	err = json.Unmarshal(body, &gu)
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

	u, err := db.Storage.GetUserOrCreateByOAuth(
		"github",
		fmt.Sprintf("%d", gu.ID),
		gu.Email,
		gu.Login,
		gu.AvatarURL)
	if err != nil {
		http.Error(w, "get/create user failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, config.Cfg.SessionDuration)

	log.Printf("user %s logged in via GitHub", u.Email)

	// If user doesn't have username, redirect to /me to complete profile
	if u.Username == "" {
		log.Printf("user %s has no username, redirecting to /me", u.Email)
		http.Redirect(w, r, config.Cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}
