package session

/*
In-memory session store (opaque SID -> db.User).
*/

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/gob"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"edev/db"
)

type session struct {
	User         db.User                  `json:"user"`
	channels     map[chan string]struct{} `json:"-"` // multiple SSE channels per session (not serialized)
	ExpiresAt    int64                    `json:"expires_at"`
	FlashMessage string                   `json:"flash_message,omitempty"`
	FlashType    string                   `json:"flash_type,omitempty"` // e.g. "success", "error", "info", ...
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

// Register SSE notifications channel (not serialized)
func RegisterSSEChannel(sid string, ch chan string) {
	// Ownership model:
	// - The SSE handler creates and OWNS the channel; it will close(ch) in its defer.
	// - The session store holds references to ALL active channels for this sid.
	// - We DO NOT close any channels here to avoid races with concurrent sends.
	sessions.Lock()
	sess, ok := sessions.m[sid]
	if ok {
		if sess.channels == nil {
			sess.channels = make(map[chan string]struct{})
		}
		sess.channels[ch] = struct{}{}
		sessions.m[sid] = sess
	}
	sessions.Unlock()
}

// SendSSENotification sends a notification message to the session's SSE channel.
func SendSSENotification(sid string, msg string) {
	// Snapshot current channels under read lock
	sessions.RLock()
	var list []chan string
	sess, ok := sessions.m[sid]
	if ok && len(sess.channels) > 0 {
		list = make([]chan string, 0, len(sess.channels))
		for ch := range sess.channels {
			list = append(list, ch)
		}
	}
	sessions.RUnlock()

	if len(list) == 0 {
		return
	}

	// Send with short timeout to reduce drops; prune closed channels
	var stale []chan string
	for _, ch := range list {
		func(ch chan string) {
			defer func() {
				if r := recover(); r != nil {
					// closed channel; mark stale for cleanup
					stale = append(stale, ch)
				}
			}()
			select {
			case ch <- msg:
				// sent
			case <-time.After(200 * time.Millisecond):
				// timed out, drop
			}
		}(ch)
	}

	if len(stale) > 0 {
		sessions.Lock()
		if sess, ok := sessions.m[sid]; ok && len(sess.channels) > 0 {
			for _, ch := range stale {
				delete(sess.channels, ch)
			}
			sessions.m[sid] = sess
		}
		sessions.Unlock()
	}
}

// BroadcastSSENotification sends a message to all active channels across all sessions.
// Returns the number of channels that accepted the message (non-blocking sends only).
func BroadcastSSENotification(msg string) int {
	// Snapshot channels under read lock
	sessions.RLock()
	var all []chan string
	if len(sessions.m) > 0 {
		all = make([]chan string, 0, len(sessions.m))
		for _, sess := range sessions.m {
			for ch := range sess.channels {
				all = append(all, ch)
			}
		}
	}
	sessions.RUnlock()

	if len(all) == 0 {
		return 0
	}

	var stale []chan string
	sent := 0
	for _, ch := range all {
		func(ch chan string) {
			defer func() {
				if r := recover(); r != nil {
					stale = append(stale, ch)
				}
			}()
			select {
			case ch <- msg:
				sent++
			case <-time.After(200 * time.Millisecond):
				// timed out, drop
			}
		}(ch)
	}

	if len(stale) > 0 {
		sessions.Lock()
		for sid, sess := range sessions.m {
			if len(sess.channels) == 0 {
				continue
			}
			for _, ch := range stale {
				delete(sess.channels, ch)
			}
			sessions.m[sid] = sess
		}
		sessions.Unlock()
	}

	return sent
}

// UnregisterSSEChannel removes the SSE notifications channel from the session.
func UnregisterSSEChannel(sid string, ch chan string) {
	sessions.Lock()
	sess, ok := sessions.m[sid]
	if ok && len(sess.channels) > 0 {
		delete(sess.channels, ch)
		sessions.m[sid] = sess
	}
	sessions.Unlock()
}

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

// ===== CSRF helpers =====

const (
	secureCSRFCookieName   = "__Host-csrf"
	insecureCSRFCookieName = "csrf"
)

// GenerateCSRFToken returns an existing CSRF token from cookie or creates a new one and sets the cookie.
// Double-submit cookie pattern: the same token is also sent back in a hidden form field named "csrf_token".
func GenerateCSRFToken(w http.ResponseWriter, r *http.Request) string {
	name := secureCSRFCookieName
	secure := !insecureCookie
	if !secure {
		name = insecureCSRFCookieName
	}

	if c, err := r.Cookie(name); err == nil && c.Value != "" {
		return c.Value
	}

	// Generate 32 random bytes, base64url without padding
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
		Expires:  time.Now().Add(24 * time.Hour),
	})
	return token
}

// ValidateCSRF compares the CSRF cookie with the form field "csrf_token" using constant-time compare.
func ValidateCSRF(r *http.Request) bool {
	name := secureCSRFCookieName
	if insecureCookie {
		name = insecureCSRFCookieName
	}
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return false
	}
	formToken := r.FormValue("csrf_token")
	if formToken == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(formToken)) != 1 {
		return false
	}
	return true
}
