package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"edev/config"
	"edev/log"
	"edev/session"
	"edev/user"
	"edev/utils"

	"golang.org/x/oauth2"
)

type XProvider struct{}

func (XProvider) config() *oauth2.Config {
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

func (p XProvider) LoginHandler(w http.ResponseWriter, r *http.Request) {
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

func (p XProvider) CallbackHandler(w http.ResponseWriter, r *http.Request) {
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

	if resp.StatusCode == http.StatusForbidden {
		log.Printf("API v2 returned 403, trying fallback to API v1.1")

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
