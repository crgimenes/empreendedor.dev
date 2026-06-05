package oauthproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/crgimenes/devengine/config"
	"github.com/crgimenes/devengine/db"
	"github.com/crgimenes/devengine/log"
	"github.com/crgimenes/devengine/session"
	"github.com/crgimenes/devengine/utils"

	"golang.org/x/oauth2"
)

type DiscordProvider struct{}

func (DiscordProvider) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.Discord.ClientID,
		ClientSecret: cfg.Discord.ClientSecret,
		RedirectURL:  config.Cfg.BaseURL + "/discord/oauth/callback",
		Scopes:       []string{"identify", "email"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://discord.com/oauth2/authorize",
			TokenURL: "https://discord.com/api/oauth2/token",
		},
	}
}

func (p DiscordProvider) LoginHandler(w http.ResponseWriter, r *http.Request) {
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

func (p DiscordProvider) CallbackHandler(w http.ResponseWriter, r *http.Request) {
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/v10/users/@me", nil)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "discord /users/@me failed: "+err.Error(), http.StatusBadGateway)
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
			fmt.Sprintf("users/@me status %d: %s", resp.StatusCode, string(b)),
			http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		http.Error(w, "read discord user failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("discord user response: %s", string(body))

	var du struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
		Email      string `json:"email"`
		Verified   bool   `json:"verified"`
	}
	err = json.Unmarshal(body, &du)
	if err != nil {
		http.Error(w, "decode user failed", http.StatusBadGateway)
		return
	}

	if du.ID == "" || du.Username == "" {
		http.Error(w, "invalid user data", http.StatusBadGateway)
		return
	}

	avatarURL := discordAvatarURL(du.ID, du.Avatar)
	username := du.Username
	email := du.Email

	log.Printf("logged in Discord user: ID=%s, Username=%s, Email=%s, AvatarURL=%s",
		du.ID, username, email, avatarURL)

	u, err := db.Storage.GetUserOrCreateByOAuth(
		"discord",
		du.ID,
		email,
		username,
		avatarURL)
	if err != nil {
		log.Printf("GetUserOrCreateByOAuth failed: %v; attempting fallback user creation", err)
		if email == "" {
			http.Error(w, "user creation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		u, err = db.Storage.CreateMinimalUserForOAuthFallback(email, avatarURL)
		if err != nil {
			log.Printf("CreateMinimalUserForOAuthFallback failed: %v", err)
			http.Error(w, "user creation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("created minimal fallback user with email %s", email)
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, config.Cfg.SessionDuration)

	log.Printf("user %s logged in via Discord", u.Email)

	if u.Email == "" {
		log.Printf("user %s has no email, redirecting to /me", u.Username)
		http.Redirect(w, r, config.Cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

func discordAvatarURL(userID, avatarHash string) string {
	if userID == "" || avatarHash == "" {
		return ""
	}
	ext := "png"
	if len(avatarHash) > 2 && avatarHash[:2] == "a_" {
		ext = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s?size=256", userID, avatarHash, ext)
}
