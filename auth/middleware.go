package auth

import (
	"net/http"
	"slices"
	"time"

	"edev/config"
	"edev/db"
	"edev/session"
)

type SessionUser interface {
	ToDBUser() db.User
}

type SessionProvider interface {
	GetCookie(*http.Request) (string, bool)
	SetCookie(http.ResponseWriter, string, time.Duration)
	Get(string) (SessionUser, bool)
	Del(string)
}

var sessions SessionProvider = sessionStore{}

func SetSessionProvider(p SessionProvider) {
	sessions = p
}

type sessionStore struct{}

type storedUser struct {
	db.User
}

func (sessionStore) GetCookie(r *http.Request) (string, bool) { return session.GetCookie(r) }

func (sessionStore) SetCookie(w http.ResponseWriter, value string, maxAge time.Duration) {
	session.SetCookie(w, value, maxAge)
}

func (sessionStore) Get(sid string) (SessionUser, bool) {
	u, ok := session.Get(sid)
	if !ok {
		return nil, false
	}
	return storedUser{User: u}, true
}

func (sessionStore) Del(sid string) { session.Del(sid) }

func (u storedUser) ToDBUser() db.User { return u.User }

func Logout(w http.ResponseWriter, r *http.Request) {
	sid, ok := sessions.GetCookie(r)
	if ok {
		sessions.Del(sid)
	}
	sessions.SetCookie(w, "", -1) // clear cookie
	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

func Prelude(
	w http.ResponseWriter,
	r *http.Request,
	allowedMethods []string,
	chkAuth bool,
	chkRatelimit bool,
	preventCache bool,
) (
	*db.User,
	string,
	bool,
	error,
) {
	if preventCache {
		h := w.Header()
		h.Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
		h.Set("Pragma", "no-cache")
		h.Set("Expires", "0")
	}

	if len(allowedMethods) > 0 {
		methodAllowed := slices.Contains(allowedMethods, r.Method)
		if !methodAllowed {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return nil, "", false, nil
		}
	}

	if !chkAuth {
		return nil, "", false, nil
	}

	if chkRatelimit {
	}

	ref := r.Referer()
	_ = ref
	origin := r.Header.Get("Origin")
	_ = origin

	sid, ok := sessions.GetCookie(r)
	if !ok {
		http.Redirect(w, r, config.Cfg.BaseURL+"/login", http.StatusFound)
		return nil, "", false, nil
	}

	su, ok := sessions.Get(sid)
	if !ok {
		http.Redirect(w, r, config.Cfg.BaseURL+"/login", http.StatusFound)
		return nil, "", false, nil
	}

	u := su.ToDBUser()
	return &u, sid, true, nil
}
