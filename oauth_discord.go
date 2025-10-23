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

type DiscordProvider struct{}

func (DiscordProvider) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     config.Cfg.DiscordClientID,
		ClientSecret: config.Cfg.DiscordClientSecret,
		RedirectURL:  config.Cfg.BaseURL + "/discord/oauth/callback",
		// Scopes required to retrieve username and email from /users/@me
		Scopes: []string{"identify", "email"},
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
	// Discord user endpoint; v10 is the current stable API.
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

	// Minimal subset of Discord user fields needed.
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
	email := du.Email // may be empty if scope not granted; handled below

	log.Printf("logged in Discord user: ID=%s, Username=%s, Email=%s, AvatarURL=%s",
		du.ID, username, email, avatarURL)

	u, err := db.Storage.GetUserOrCreateByOAuth(
		"discord",
		du.ID,
		email,
		username,
		avatarURL)
	if err != nil {
		http.Error(w, "get/create user failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, config.Cfg.SessionDuration)

	log.Printf("user %s logged in via Discord", u.Email)

	// If user does not have email, redirect to /me to complete profile (email is essential)
	if u.Email == "" {
		log.Printf("user %s has no email, redirecting to /me", u.Username)
		http.Redirect(w, r, config.Cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

// discordAvatarURL builds the CDN avatar URL for a Discord user given id and avatar hash.
// If avatar hash is empty, returns empty string (consumer may fall back to default avatar).
func discordAvatarURL(userID, avatarHash string) string {
	if userID == "" || avatarHash == "" {
		return ""
	}
	ext := "png"
	if len(avatarHash) > 2 && avatarHash[:2] == "a_" {
		ext = "gif"
	}
	// 256 size is reasonable for avatars; Discord supports size query param.
	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s?size=256", userID, avatarHash, ext)
}
