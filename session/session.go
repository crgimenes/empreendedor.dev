package session

/*
In-memory session store (opaque SID -> db.User).
*/

import (
	"bytes"
	"edev/db"
	"encoding/gob"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type session struct {
	User         db.User `json:"user"`
	ExpiresAt    int64   `json:"expires_at"`
	FlashMessage string  `json:"flash_message,omitempty"`
	FlashType    string  `json:"flash_type,omitempty"` // e.g. "success", "error", "info", ...
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

func Serialize() ([]byte, error) {
	sessions.RLock()
	defer sessions.RUnlock()

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(sessions.m)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Deserialize(b []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(b))
	s := make(map[string]session)
	err := dec.Decode(&s)
	if err != nil {
		return err
	}

	sessions.Lock()
	sessions.m = s
	sessions.Unlock()
	return nil
}

func LoadFromGobFile(filename string) error {
	bb, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("No existing session file found, starting fresh")
			return nil
		}
		return err
	}
	err = Deserialize(bb)
	if err != nil {
		log.Printf("Session deserialization error: %v", err)
		return err
	}
	log.Printf("Restored %d sessions from file", Count())

	return nil
}

func SaveToGobFile(filename string) error {
	ss, err := Serialize()
	if err != nil {
		return err
	}

	err = os.WriteFile(filename, ss, 0600)
	if err != nil {
		return err
	}

	log.Printf("Saved %d sessions to file", Count())
	return nil
}

func Count() int {
	sessions.RLock()
	n := len(sessions.m)
	sessions.RUnlock()
	return n
}

func Put(sid string, u db.User) {
	s := session{
		User:      u,
		ExpiresAt: time.Now().Unix() + MaxSessionAge,
	}
	sessions.Lock()
	sessions.m[sid] = s
	sessions.Unlock()
}

func Get(sid string) (db.User, bool) {
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

// SyncSessions updates all sessions for the same user as in the given session ID.
func SyncSessions(sid string) {
	sessions.RLock()
	s, ok := sessions.m[sid]
	sessions.RUnlock()
	if !ok {
		return
	}

	sessions.Lock()
	for key, sess := range sessions.m {
		if key == sid {
			continue
		}
		if sess.User.ID == s.User.ID {
			sess.User = s.User
			sessions.m[key] = sess
		}
	}
	sessions.Unlock()
}

// ===== Cookie helpers =====

// Cookie helpers
// In secure (default) mode we use the __Host- prefix which requires Secure=true, Path=/ and no Domain.
// When insecureCookie is enabled (local dev over http) we must NOT use the __Host- prefix because
// browsers will silently reject a cookie whose name starts with __Host- if Secure is false.
const (
	secureSessCookieName   = "__Host-sid"
	insecureSessCookieName = "sid"
)

var insecureCookie bool

// EnableInsecureCookie enables non-Secure cookies (DEV/TEST only). Not for production use.
func EnableInsecureCookie() { insecureCookie = true }

func SetCookie(w http.ResponseWriter, value string, maxAge time.Duration) {
	secure := !insecureCookie
	name := secureSessCookieName
	if !secure {
		name = insecureSessCookieName
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
		Expires:  time.Now().Add(maxAge),
	})
}

func GetCookie(r *http.Request) (string, bool) {
	name := secureSessCookieName
	if insecureCookie {
		name = insecureSessCookieName
	}
	c, err := r.Cookie(name)
	if err != nil {
		return "", false
	}
	return c.Value, true
}
