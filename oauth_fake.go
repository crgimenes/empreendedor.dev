package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"edev/config"
	"edev/db"
	"edev/log"
	"edev/session"
	"edev/utils"
)

// FakeProvider integrates with the local fake OAuth server (cmd/fakeoauth) for development/testing.
type FakeProvider struct{}

func (FakeProvider) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state := utils.NewOpaqueID()
	verifier, challenge := utils.MakePKCE()
	putState(state, verifier, 5*time.Minute)
	redir := config.Cfg.FakeOAuthBaseURL + "/oauth/authorize?response_type=code&client_id=" +
		url.QueryEscape(config.Cfg.FakeOAuthClientID) +
		"&redirect_uri=" + url.QueryEscape(config.Cfg.BaseURL+config.Cfg.FakeOAuthRedirect) +
		"&scope=profile+email&state=" + url.QueryEscape(state) +
		"&code_challenge=" + url.QueryEscape(challenge) + "&code_challenge_method=S256"
	http.Redirect(w, r, redir, http.StatusFound)
}

func (FakeProvider) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	recvState := r.URL.Query().Get("state")
	if recvState == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	verifier, ok := takeState(recvState)
	if !ok {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", config.Cfg.BaseURL+config.Cfg.FakeOAuthRedirect)
	form.Set("client_id", config.Cfg.FakeOAuthClientID)
	form.Set("code_verifier", verifier)
	resp, err := http.Post(config.Cfg.FakeOAuthBaseURL+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("close resp body: %v", cerr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "token exchange status", http.StatusBadGateway)
		return
	}
	var tokResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokResp); err != nil {
		http.Error(w, "decode token", http.StatusBadGateway)
		return
	}
	if tokResp.AccessToken == "" {
		http.Error(w, "empty access_token", http.StatusBadGateway)
		return
	}
	// userinfo
	req, _ := http.NewRequest("GET", config.Cfg.FakeOAuthBaseURL+"/oauth/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tokResp.AccessToken)
	uiResp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "userinfo failed", http.StatusBadGateway)
		return
	}
	defer func() {
		if cerr := uiResp.Body.Close(); cerr != nil {
			log.Printf("close userinfo body: %v", cerr)
		}
	}()
	if uiResp.StatusCode != http.StatusOK {
		http.Error(w, "userinfo status", http.StatusBadGateway)
		return
	}
	var raw map[string]any
	if err := json.NewDecoder(uiResp.Body).Decode(&raw); err != nil {
		http.Error(w, "decode userinfo", http.StatusBadGateway)
		return
	}

	u, err := db.Storage.GetUserOrCreateByOAuth(
		"fakeoauth",
		fmt.Sprintf("%v", raw["id"]),
		fmt.Sprintf("%v", raw["email"]),
		fmt.Sprintf("%v", raw["login"]),
		fmt.Sprintf("%v", raw["avatar_url"]))
	if err != nil {
		http.Error(w, "get/create user failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, config.Cfg.SessionDuration)

	log.Printf("user %s logged in via fake OAuth", u.Email)

	// If user doesn't have username, redirect to /me to complete profile
	if u.Username == "" {
		log.Printf("user %s has no username, redirecting to /me", u.Email)
		http.Redirect(w, r, config.Cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}
