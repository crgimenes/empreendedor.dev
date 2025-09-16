package session

/*
In-memory session store (opaque SID -> user.User).
*/

import (
	"net/http"
	"sync"
	"time"

	"github.com/crgimenes/empreendedor.dev/user"
)

type session struct {
	User      user.User
	ExpiresAt int64
}

var (
	sessions = struct {
		sync.RWMutex
		m map[string]session
	}{
		m: make(map[string]session),
	}

	MaxSessionAge = int64(3600 * 3) // 3 hours in seconds
)

func Put(sid string, u user.User) {
	s := session{
		User:      u,
		ExpiresAt: time.Now().Unix() + MaxSessionAge,
	}
	sessions.Lock()
	sessions.m[sid] = s
	sessions.Unlock()
}

func Get(sid string) (user.User, bool) {
	sessions.RLock()
	s, ok := sessions.m[sid]
	sessions.RUnlock()
	return s.User, ok
}

func Del(sid string) {
	sessions.Lock()
	delete(sessions.m, sid)
	sessions.Unlock()
}

func Cleanup() {
	if len(sessions.m) == 0 {
		return
	}
	now := time.Now().Unix()
	sessions.Lock()
	for sid, s := range sessions.m {
		if s.ExpiresAt < now {
			delete(sessions.m, sid)
		}
	}
	sessions.Unlock()
}

// ===== Cookie helpers =====

// Cookie helpers
const sessCookie = "__Host-sid"

func SetCookie(w http.ResponseWriter, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
		Expires:  time.Now().Add(maxAge),
	})
}

func GetCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessCookie)
	if err != nil {
		return "", false
	}
	return c.Value, true
}
