package oauthproviders

import (
	"net/http"
	"sync"
	"time"
)

type ProviderCreds struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
}

type Config struct {
	Discord ProviderCreds
	GitHub  ProviderCreds
}

var cfg Config

func Setup(c Config) { cfg = c }

func Routes(mux *http.ServeMux) {
	if cfg.Discord.Enabled {
		mux.HandleFunc("/login/discord", DiscordProvider{}.LoginHandler)
		mux.HandleFunc("/discord/oauth/callback", DiscordProvider{}.CallbackHandler)
	}
	if cfg.GitHub.Enabled {
		mux.HandleFunc("/login/github", GitHubProvider{}.LoginHandler)
		mux.HandleFunc("/github/oauth/callback", GitHubProvider{}.CallbackHandler)
	}
}

type stateEntry struct {
	Verifier string
	Expires  time.Time
}

var states = struct {
	sync.Mutex
	m map[string]stateEntry
}{m: make(map[string]stateEntry)}

func putState(st, verifier string, ttl time.Duration) {
	states.Lock()
	states.m[st] = stateEntry{Verifier: verifier, Expires: time.Now().Add(ttl)}
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
