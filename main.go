package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"edev/assets"
	"edev/config"
	"edev/db"
	"edev/filemanager"
	"edev/log"
	"edev/lua"
	"edev/mail"
	"edev/migration"
	"edev/session"
	"edev/templates"
	"edev/utils"
)

type stateEntry struct {
	Verifier string
	Expires  time.Time
}

var (
	GitTag = "dev"
	states = struct {
		sync.Mutex
		m map[string]stateEntry
	}{m: make(map[string]stateEntry)}
)

func securityHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"form-action 'self'",
		"object-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https: *.githubusercontent.com github.com *.twimg.com pbs.twimg.com",
		"frame-ancestors 'none'",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		//w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", csp)

		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}

type respWriter struct {
	http.ResponseWriter
	status int
}

func (w *respWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := prelude(w, r,
		[]string{
			http.MethodGet,
			http.MethodHead,
		},
		false, // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "0")
		w.Write([]byte{})
		return
	}

	sid, ok := session.GetCookie(r)
	u := db.User{}
	authed := false
	if ok {
		if got, ok := session.Get(sid); ok {
			u, authed = got, true
		}
	}

	// TODO: get message from session flash messages
	// Check for message in query parameter
	message := r.URL.Query().Get("message")
	if len(message) > 200 {
		log.Printf("message too long, truncating")
		http.Error(w, "message too long", http.StatusBadRequest)
		return
	}

	data := struct {
		Authed  bool
		User    db.User
		Error   string
		Message string
		Config  config.Config
	}{
		Authed:  authed,
		User:    u,
		Message: message,
		Config:  *config.Cfg,
	}

	err = templates.ExecuteTemplate(w, "index.go.tmpl", data)
	if err != nil {
		log.Printf("template %s execute error: %v", "index.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func loginPageHandler(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := prelude(w, r,
		[]string{
			http.MethodGet,
		},
		false, // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// If already authenticated, redirect to home
	sid, ok := session.GetCookie(r)
	if ok {
		_, ok := session.Get(sid)
		if ok {
			http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
			return
		}
	}

	var u db.User
	authed := false
	u, authed = session.Get(sid)

	data := struct {
		Authed  bool
		User    db.User
		Error   string
		Message string
		Config  config.Config
	}{
		Authed: authed,
		User:   u,
		Config: *config.Cfg,
	}

	err = templates.ExecuteTemplate(w, "login.go.tmpl", data)
	if err != nil {
		log.Printf("template %s execute error: %v", "login.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		panic(err)
	}
	return true
}

func ifEmpty(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func runLuaFile(name string) {
	// Create a new Lua state.
	L := lua.New()
	defer L.Close()

	L.SetGlobal("GitTag", ifEmpty(GitTag, config.Cfg.GitTag))
	L.SetGlobal("BaseURL", ifEmpty(os.Getenv("BASE_URL"), config.Cfg.BaseURL))
	L.SetGlobal("Address", ifEmpty(os.Getenv("ADDRESS"), config.Cfg.Addrs))

	L.SetGlobal("DiscordOAuthEnabled", os.Getenv("DISCORD_OAUTH_ENABLED") == "true")
	L.SetGlobal("DiscordClientID", os.Getenv("DISCORD_CLIENT_ID"))
	L.SetGlobal("DiscordClientSecret", os.Getenv("DISCORD_CLIENT_SECRET"))

	L.SetGlobal("GithubOAuthEnabled", os.Getenv("GITHUB_OAUTH_ENABLED") == "true")
	L.SetGlobal("GitHubClientID", os.Getenv("GITHUB_CLIENT_ID"))
	L.SetGlobal("GitHubClientSecret", os.Getenv("GITHUB_CLIENT_SECRET"))

	L.SetGlobal("XOAuthEnabled", os.Getenv("X_OAUTH_ENABLED") == "true")
	L.SetGlobal("XClientID", os.Getenv("X_CLIENT_ID"))
	L.SetGlobal("XClientSecret", os.Getenv("X_CLIENT_SECRET"))

	L.SetGlobal("FakeOAuthEnabled", os.Getenv("FAKE_OAUTH_ENABLED") == "true")
	L.SetGlobal("FakeOAuthBaseURL", ifEmpty(
		os.Getenv("FAKE_OAUTH_BASE_URL"), config.Cfg.FakeOAuthBaseURL))
	L.SetGlobal("FakeOAuthClientID", ifEmpty(
		os.Getenv("FAKE_OAUTH_CLIENT_ID"), config.Cfg.FakeOAuthClientID))
	L.SetGlobal("FakeOAuthRedirectPath", ifEmpty(
		os.Getenv("FAKE_OAUTH_REDIRECT_PATH"), config.Cfg.FakeOAuthRedirect))

	L.SetGlobal("DBFile", ifEmpty(
		os.Getenv("DB_FILE"), config.Cfg.DBFile))

	L.SetGlobal("ResendAPIKey", ifEmpty(
		os.Getenv("RESEND_API_KEY"), config.Cfg.ResendAPIKey))

	// Read the Lua file.
	b, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		log.Fatal(err)
	}

	err = L.DoString(string(b))
	if err != nil {
		log.Fatal(err)
	}

	config.Cfg.Addrs = L.MustGetString("Address")
	config.Cfg.BaseURL = L.MustGetString("BaseURL")
	config.Cfg.FakeOAuthEnabled = L.MustGetBool("FakeOAuthEnabled")

	config.Cfg.DiscordOAuthEnabled = L.MustGetBool("DiscordOAuthEnabled")
	config.Cfg.DiscordClientID = L.MustGetString("DiscordClientID")
	config.Cfg.DiscordClientSecret = L.MustGetString("DiscordClientSecret")

	config.Cfg.GithubOAuthEnabled = L.MustGetBool("GithubOAuthEnabled")
	config.Cfg.GitHubClientID = L.MustGetString("GitHubClientID")
	config.Cfg.GitHubClientSecret = L.MustGetString("GitHubClientSecret")
	config.Cfg.GitTag = L.MustGetString("GitTag")

	config.Cfg.XOAuthEnabled = L.MustGetBool("XOAuthEnabled")
	config.Cfg.XClientID = L.MustGetString("XClientID")
	config.Cfg.XClientSecret = L.MustGetString("XClientSecret")
	config.Cfg.DBFile = L.MustGetString("DBFile")

	if config.Cfg.FakeOAuthEnabled {

		session.EnableInsecureCookie()

		config.Cfg.FakeOAuthBaseURL = L.MustGetString("FakeOAuthBaseURL")
		config.Cfg.FakeOAuthClientID = L.MustGetString("FakeOAuthClientID")
		config.Cfg.FakeOAuthRedirect = L.MustGetString("FakeOAuthRedirectPath")
	}

	config.Cfg.ResendAPIKey = L.MustGetString("ResendAPIKey")
}

func putState(st, verifier string, ttl time.Duration) {
	states.Lock()
	states.m[st] = stateEntry{Verifier: verifier, Expires: time.Now().Add(ttl)}
	// simple opportunistic cleanup:
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

// OAuth provider instances (defined in separate files)
var (
	gitHubProvider  = GitHubProvider{}
	discordProvider = DiscordProvider{}
	xProvider       = XProvider{}
	fakeProvider    = FakeProvider{}
)

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if sid, ok := session.GetCookie(r); ok {
		session.Del(sid)
	}
	session.SetCookie(w, "", -1) // clear cookie
	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
}

// prelude checks authentication and returns the user or redirects to login.
func prelude(
	w http.ResponseWriter,
	r *http.Request,
	allowedMethods []string,
	chkAuth bool,
	chkRatelimit bool,
	preventCache bool,
) (
	*db.User,
	string, // session id
	bool, // authenticated
	error) {
	if preventCache {
		w.Header().Set("Cache-Control", "private, no-cache")
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
		// not implemented yet
	}

	ref := r.Referer()
	log.Printf("referer: %q", ref)
	origin := r.Header.Get("Origin")
	log.Printf("origin: %q", origin)

	/*
		// check referer
		// TODO: mote to use in file sharing links
		urlBase := strings.TrimPrefix(config.Cfg.BaseURL, "https://")
		urlBase = strings.TrimPrefix(urlBase, "http://")
		if ref != "" && !strings.Contains(ref, urlBase) {
			http.Error(w, "forbidden", http.StatusForbidden)
			log.Printf("forbidden referer: %s", ref)
			return nil, "", false, nil
		}
	*/

	// check session
	sid, ok := session.GetCookie(r)
	if !ok {
		http.Redirect(w, r, config.Cfg.BaseURL+"/login", http.StatusFound)
		return nil, "", false, nil
	}

	u, ok := session.Get(sid)
	if !ok {
		http.Redirect(w, r, config.Cfg.BaseURL+"/login", http.StatusFound)
		return nil, "", false, nil
	}

	return &u, sid, true, nil
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	u, sid, authed, err := prelude(w, r,
		[]string{
			http.MethodGet,
			http.MethodPost,
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // prelude already handled redirect
	}

	if r.Method == "GET" {
		// Show profile form
		data := struct {
			Authed  bool
			User    db.User
			Error   string
			Message string
			Config  config.Config
		}{
			Authed: true,
			User:   *u,
			Config: *config.Cfg,
		}
		err := templates.ExecuteTemplate(w, "me.go.tmpl", data)
		if err != nil {
			log.Printf("template %s execute error: %v", "me.go.tmpl", err)
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	if r.Method == "POST" {
		// Process profile update
		err := r.ParseMultipartForm(10 << 20) // 10 MB
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		username := r.FormValue("username") // TODO: Prevent username format abuse
		//avatarURL := r.FormValue("avatar_url")
		avatarURL := u.AvatarURL // keep existing if no new file uploaded

		/// get files from form
		file, fh, err := r.FormFile("avatar_file")
		if err != nil && err != http.ErrMissingFile {
			log.Printf("error getting avatar file from form: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		defer func() {
			if file != nil {
				file.Close()
			}
		}()

		if file != nil {
			log.Printf("uploaded avatar file: %v", fh.Filename)

			// TODO: ensure same-origin upload (CSRF protection)
			typeDetected, size, err := filemanager.ValidateFile(
				file,
				fh,
				[]string{"image/jpeg", "image/png", "image/gif", "image/webp"},
				[]string{"jpg", "jpeg", "png", "gif", "webp"},
				5<<20, // 5 MB
			)
			if err != nil {
				// TODO: Return error as form error message using alert message from Bootstrap
				//   - ErrorFileNameInvalid: File name is too long (>255 chars) or contains invalid characters
				//   - ErrorFileExtension: File extension is not in the accepted list
				//   - ErrorFileTooLarge: File size exceeds the maximum limit
				//   - ErrorFileRead: Error occurred while reading the file
				//   - ErrorInvalidFileType: Detected MIME type is not in the accepted list
				//
				log.Printf("avatar file validation error: %v", err)
				http.Error(w, "invalid avatar file: "+err.Error(), http.StatusBadRequest)
				return
			}

			log.Printf("avatar file validated: name %q type=%q, size=%d",
				fh.Filename,
				typeDetected,
				size)

			// process uploaded file
			log.Printf("processing uploaded avatar file: %v", fh.Filename)
			// For simplicity, we just read the file and simulate uploading it
			// In a real application, you would store it in a storage service
			avatarData := make([]byte, fh.Size)
			_, err = file.Read(avatarData)
			if err != nil {
				log.Printf("error reading uploaded avatar file: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			// TODO: use filehash to avoid duplicate uploads (duoplicate metadata entries but same file on disk)

			// Get user uploads directory
			uploadsDir, err := filemanager.DataFilePath(u)
			if err != nil {
				log.Printf("error getting user data file path: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			// TODO: save file in temp dir and convert/resize to standard sizes and formats

			fileExt := strings.ToLower(filepath.Ext(fh.Filename))
			avatarPath := filepath.Join(uploadsDir, filemanager.FileName()+fileExt)

			log.Printf("saving uploaded avatar file to: %q", avatarPath)

			// Save file to disk in the user's upload directory
			err = os.WriteFile(avatarPath, avatarData, 0600)
			if err != nil {
				log.Printf("error saving uploaded avatar file: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			fileHash, err := filemanager.FileHash(file)
			if err != nil {
				log.Printf("error calculating file hash: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			fileMeta := &db.File{
				UserID:           u.ID,
				OriginalFilename: fh.Filename,
				Filename:         filepath.Base(avatarPath),
				Filepath:         avatarPath, // real path on disk
				Filesize:         size,
				Filetype:         typeDetected,
				Filehash:         fileHash,
				Filetag:          "avatar",
				Filedescription:  "User avatar image",
				Processed:        false,
				CreatedAt:        time.Now().UTC().Format(time.RFC3339),
				UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
			}

			fileMeta, err = filemanager.SaveFileMetadata(fileMeta)
			if err != nil {
				log.Printf("error saving file metadata: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			avatarURL = "/file/" + u.ReferenceID + "/" + fileMeta.Filename
			log.Printf("avatar file saved: %s", avatarURL)
		}

		// Update user profile
		updatedUser, err := db.Storage.UpdateUserProfile(u.ID, username, avatarURL)
		if err != nil {
			log.Printf("error updating user profile: %v", err)
			// Re-render form with error
			data := struct {
				Authed  bool
				User    db.User
				Error   string
				Message string
				Config  config.Config
			}{
				Authed: true,
				User:   *u,
				Error:  err.Error(),
				Config: *config.Cfg,
			}
			err2 := templates.ExecuteTemplate(w, "me.go.tmpl", data)
			if err2 != nil {
				log.Printf("template %s execute error: %v", "me.go.tmpl", err2)
			}
			return
		}

		// Update session with new user data
		session.Put(sid, *updatedUser)
		session.SyncSessions(sid)

		log.Printf("user %s updated profile", updatedUser.Email)

		// Redirect to home
		http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// serve user files
func fileHandler(w http.ResponseWriter, r *http.Request) {

	u, _, authed, err := prelude(w, r,
		[]string{
			http.MethodGet,
		},
		true,  // check auth
		false, // check ratelimit
		false, // prevent cache
	)
	if err != nil {
		log.Printf("prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // prelude already handled redirect
	}

	log.Printf("serving file %q for user %s", r.URL.Path, u.Email)

	path := strings.TrimPrefix(r.URL.Path, config.Cfg.BaseURL+"/file/")
	path = strings.TrimPrefix(path, "/file/")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid file path", http.StatusBadRequest)
		return
	}

	// Another user can access their files if they have the link, which is by design
	userRefID := parts[0]
	filename := parts[1]

	err = filemanager.ValidateFilename(filename)
	if err != nil {
		log.Printf("invalid filename: %v", err)
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	fileMeta, err := filemanager.GetFileByUserReferenceIDAndFilename(userRefID, filename)
	if err != nil {
		log.Printf("error getting file metadata: %v", err)
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// TODO: implement cache validation using ETag and Last-Modified headers based on fileMeta.Filehash

	http.ServeFile(w, r, fileMeta.Filepath)
}

func linkHandler(w http.ResponseWriter, r *http.Request) {
	u, _, _, err := prelude(w, r,
		[]string{
			http.MethodGet,
		},
		false, // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	token := r.PathValue("token")

	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	if !utils.ValidateOpaqueID(token) {
		http.Error(w, "invalid token format", http.StatusBadRequest)
		return
	}

	email, err := db.Storage.ConsumeMagicLinkToken(token)
	if err != nil {
		log.Printf("error consuming magic link token: %v", err)
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}
	if email == "" {
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}

	u, err = db.Storage.GetUserOrCreateByEmail(email)
	if err != nil {
		log.Printf("error getting or creating user by email: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, config.Cfg.SessionDuration)

	log.Printf("user %s logged in via magic link", u.Email)

	if u.Username == "" {
		log.Printf("user %s has no username, redirecting to /me", u.Email)
		// redirect to complete profile
		http.Redirect(w, r, config.Cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	// redirect to home
	http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)

}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// check session - session is required for SSE
	sid, ok := session.GetCookie(r)
	if !ok {
		log.Printf("SSE: No session cookie found")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	u, ok := session.Get(sid)
	if !ok {
		log.Printf("SSE: Session %s not found in store", sid[:min(8, len(sid))])
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// user must be enabled to use SSE
	if !u.Enabled {
		log.Printf("SSE: User %s is not enabled (enabled=%v)", u.Email, u.Enabled)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	log.Printf("SSE connection opened for user %s (session %s)", u.Email, sid[:8])

	// Disable write deadline for SSE connections
	rc := http.NewResponseController(w)
	if rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	// Set headers for SSE - critical for client reconnection behavior
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Keep-Alive", "timeout=60")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	// Ensure response writer supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send initial SSE directives/data to confirm connection on client
	// Optional retry directive (client default is fine, but explicit is ok)
	_, _ = fmt.Fprintf(w, "retry: 10000\n") // suggest 10s retry if client reconnects
	_, _ = fmt.Fprintf(w, "data: ready\n\n")
	flusher.Flush()

	// Create channel for this session with a larger buffer to absorb short bursts
	ch := make(chan string, 64)
	session.RegisterSSEChannel(sid, ch)

	defer func() {
		session.UnregisterSSEChannel(sid, ch)
		close(ch)
		log.Printf("SSE connection closed for user %s", u.Email)
	}()

	ctx := r.Context()
	// Many proxies use a 30s idle timeout; send heartbeat sooner to avoid drop
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// periodic heartbeat to keep connection alive (prevents timeout by proxy/firewall)
	// and allows detecting client disconnection
	for {
		select {
		case <-ctx.Done():
			// Client disconnected or connection timeout
			log.Printf("SSE context done for user %s: %v", u.Email, ctx.Err())
			return

		case <-ticker.C:
			// Send heartbeat as data event so clients see activity and proxies keep stream
			_, err := fmt.Fprintf(w, "data: heartbeat\n\n")
			if err != nil {
				log.Printf("SSE heartbeat write error: %v", err)
				return
			}
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				// Channel closed, connection shutting down
				return
			}

			// Format message according to SSE spec
			// Format: event: type\ndata: payload\n\n
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
			if err != nil {
				log.Printf("SSE write error: %v", err)
				return
			}
			flusher.Flush()
			log.Printf("SSE message sent to user %s: %q", u.Email, msg)
		}
	}
}

func handlerLoginMagic(w http.ResponseWriter, r *http.Request) {
	// prelude
	_, _, _, err := prelude(w, r,
		[]string{
			http.MethodPost,
		},
		false, // check auth
		false, // TODO: check ratelimit to prevent abuse
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	email := r.FormValue("email")
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}

	// Clamp email length to prevent abuse
	if len(email) > 254 {
		http.Error(w, "email too long", http.StatusBadRequest)
		return
	}

	email, err = mail.CanonicalizeEmail(email)
	if err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	// generate a random token
	token := utils.NewOpaqueID()

	// store the token with the email and expiration (15 minutes)
	err = db.Storage.StoreMagicLinkToken(token, email, time.Now().UTC().Add(15*time.Minute))
	if err != nil {
		log.Printf("error storing magic link token: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// send the email with the link
	link := config.Cfg.BaseURL + "/link/" + token
	ret, err := mail.Send(mail.EmailRequest{
		From:    "noreply@" + strings.TrimPrefix(config.Cfg.BaseURL, "https://"),
		To:      []string{email},
		Subject: "Seu link de acesso magico",
		Text: "Clique no link para fazer login:\n\t" +
			link +
			"\n\nEste link expira em 15 minutos.\n--\n",
	})
	if err != nil {
		log.Printf("error sending magic link email: %v", err)
		// do not reveal the error to the user
	}

	log.Printf("sent magic link email to %s, id=%s", email, ret)

	// Return redirect URL
	redirectURL := config.Cfg.BaseURL +
		"/?message=" +
		url.QueryEscape("Link de acesso enviado! Verifique seu email.")

	http.Redirect(w, r, redirectURL, http.StatusFound)

}

func main() {
	config.Cfg.GitTag = GitTag

	const initLua = "init.lua"

	if !fileExists(initLua) {
		log.Fatal("init.lua not found")
	}

	runLuaFile(initLua)

	var err error

	db.Storage, err = db.New()
	if err != nil {
		log.Fatalf("Error on db: %s", err)
	}

	err = migration.Run()
	if err != nil {
		log.Fatalf("Migration error: %v", err)
	}

	go func() {
		for {
			time.Sleep(1 * time.Hour)
			err := db.Storage.PurgeExpiredMagicLinkTokens()
			if err != nil {
				log.Printf("Error purging expired magic link tokens: %v", err)
			}
		}
	}()

	// Load sessions from file
	err = session.LoadFromGobFile("sessions.gob")
	if err != nil {
		log.Printf("Session load error: %v", err)
		return
	}

	mux := http.NewServeMux()

	fileServer := http.FileServer(assets.FS)
	mux.Handle("/assets/", http.StripPrefix("/assets/", fileServer))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// some browsers do not support link rel="icon"
		// redirect to the one served from /assets/
		http.Redirect(w, r, "/assets/favicon.ico", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/login", loginPageHandler)
	mux.HandleFunc("POST /login/magic_link", handlerLoginMagic) // for email link login and magic link

	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("GET /link/{token}", linkHandler) // for email link login and magic link

	if config.Cfg.DiscordOAuthEnabled {
		mux.HandleFunc("/login/discord", discordProvider.LoginHandler)
		mux.HandleFunc("/discord/oauth/callback", discordProvider.CallbackHandler)
	}

	if config.Cfg.GithubOAuthEnabled {
		mux.HandleFunc("/login/github", gitHubProvider.LoginHandler)
		mux.HandleFunc("/github/oauth/callback", gitHubProvider.CallbackHandler)
	}

	if config.Cfg.XOAuthEnabled {
		mux.HandleFunc("/login/x", xProvider.LoginHandler)
		mux.HandleFunc("/x/oauth/callback", xProvider.CallbackHandler)
	}

	if config.Cfg.FakeOAuthEnabled {
		mux.HandleFunc("/login/fake", fakeProvider.LoginHandler)
		mux.HandleFunc(config.Cfg.FakeOAuthRedirect, fakeProvider.CallbackHandler)
	}

	mux.HandleFunc("/logout", logoutHandler)
	mux.HandleFunc("/me", meHandler) // user profile

	mux.HandleFunc("/events", sseHandler) // server-sent events

	// Debug broadcast endpoint (unsafe, for manual testing only)
	mux.HandleFunc("/msg", func(w http.ResponseWriter, r *http.Request) {
		msg := r.URL.Query().Get("msg")
		if msg == "" {
			msg = "debug"
		}
		n := session.BroadcastSSENotification(msg)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "sent to %d channels\n", n)
	})

	// filemanager routes (user files, images, etc.)
	mux.HandleFunc("/file/", fileHandler)

	// ------------------------------------------
	srv := &http.Server{
		Addr:              config.Cfg.Addrs,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0, // ***CRITICAL*** disable write timeout for long-lived connections (SSE)
		IdleTimeout:       60 * time.Second,
	}

	// Start server in a goroutine to enable graceful shutdown below.
	go func() {
		log.Printf("Serving on %s", config.Cfg.Addrs)
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe error: %v", err)
		}
	}()

	// session Cleanup
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			session.Cleanup()
		}
	}()

	// Graceful shutdown on Ctrl+C (SIGINT).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second)
	defer cancel()

	// Save sessions to file
	err = session.SaveToGobFile("sessions.gob")
	if err != nil {
		log.Printf("Session save error: %v", err)
	}

	err = srv.Shutdown(ctx)
	if err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	if db.Storage != nil {
		db.Storage.Close()
	}
	log.Println("Server stopped.")
}
