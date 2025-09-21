package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/crgimenes/empreendedor.dev/config"
	"github.com/crgimenes/empreendedor.dev/session"
	"github.com/crgimenes/empreendedor.dev/user"
	"github.com/crgimenes/empreendedor.dev/utils"
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
		Scopes:       []string{"read:user"},
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
