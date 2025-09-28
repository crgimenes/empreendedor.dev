package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"edev/config"
	"edev/log"
	"edev/session"
	"edev/user"
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
	var raw map[string]string
	if err := json.NewDecoder(uiResp.Body).Decode(&raw); err != nil {
		http.Error(w, "decode userinfo", http.StatusBadGateway)
		return
	}
	sid := utils.NewOpaqueID()
	session.Put(sid, user.User{ID: raw["id"], Login: raw["username"], Name: raw["name"], AvatarURL: raw["avatar_url"]})
	session.SetCookie(w, sid, 8*time.Hour)
	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}
