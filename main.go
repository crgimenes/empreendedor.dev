package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
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
	"edev/forum"
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
	GitTag = "dev" + time.Now().UTC().Format("-20060102-150405")
	states = struct {
		sync.Mutex
		m map[string]stateEntry
	}{m: make(map[string]stateEntry)}
)

// Precomputed embedded assets metadata and content
type assetMeta struct {
	ETag        string
	ModTime     time.Time
	Size        int64
	ContentType string
	Data        []byte
}

var assetsIndex map[string]assetMeta

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
	// TODO: if path is not "/", return 404

	log.Printf("indexHandler: %s %s", r.Method, r.URL.Path)
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
		// No body for HEAD responses
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

	// Choose template based on authentication status
	templateName := "index.go.tmpl"
	if authed {
		templateName = "dashboard.go.tmpl"
	}

	err = templates.ExecuteTemplate(w, templateName, data)
	if err != nil {
		log.Printf("template %s execute error: %v", templateName, err)
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
	L.SetGlobal("BaseURL", ifEmpty(os.Getenv("EDEV_BASE_URL"), config.Cfg.BaseURL))
	L.SetGlobal("Address", ifEmpty(os.Getenv("EDEV_ADDRESS"), config.Cfg.Addrs))

	L.SetGlobal("DiscordOAuthEnabled", os.Getenv("EDEV_DISCORD_OAUTH_ENABLED") == "true")
	L.SetGlobal("DiscordClientID", os.Getenv("EDEV_DISCORD_CLIENT_ID"))
	L.SetGlobal("DiscordClientSecret", os.Getenv("EDEV_DISCORD_CLIENT_SECRET"))

	L.SetGlobal("GithubOAuthEnabled", os.Getenv("EDEV_GITHUB_OAUTH_ENABLED") == "true")
	L.SetGlobal("GitHubClientID", os.Getenv("EDEV_GITHUB_CLIENT_ID"))
	L.SetGlobal("GitHubClientSecret", os.Getenv("EDEV_GITHUB_CLIENT_SECRET"))

	L.SetGlobal("XOAuthEnabled", os.Getenv("EDEV_X_OAUTH_ENABLED") == "true")
	L.SetGlobal("XClientID", os.Getenv("EDEV_X_CLIENT_ID"))
	L.SetGlobal("XClientSecret", os.Getenv("EDEV_X_CLIENT_SECRET"))

	L.SetGlobal("FakeOAuthEnabled", os.Getenv("EDEV_FAKE_OAUTH_ENABLED") == "true")
	L.SetGlobal("FakeOAuthBaseURL", ifEmpty(
		os.Getenv("EDEV_FAKE_OAUTH_BASE_URL"), config.Cfg.FakeOAuthBaseURL))
	L.SetGlobal("FakeOAuthClientID", ifEmpty(
		os.Getenv("EDEV_FAKE_OAUTH_CLIENT_ID"), config.Cfg.FakeOAuthClientID))
	L.SetGlobal("FakeOAuthRedirectPath", ifEmpty(
		os.Getenv("EDEV_FAKE_OAUTH_REDIRECT_PATH"), config.Cfg.FakeOAuthRedirect))

	L.SetGlobal("DBFile", ifEmpty(
		os.Getenv("EDEV_DB_FILE"), config.Cfg.DBFile))

	L.SetGlobal("ResendAPIKey", ifEmpty(
		os.Getenv("EDEV_RESEND_API_KEY"), config.Cfg.ResendAPIKey))

	L.SetGlobal("EmailDomain", ifEmpty(
		os.Getenv("EDEV_EMAIL_DOMAIN"), config.Cfg.EmailDomain))

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
	config.Cfg.EmailDomain = L.MustGetString("EmailDomain")

	if config.Cfg.BaseURL == "http://localhost:3210" ||
		strings.HasPrefix(config.Cfg.BaseURL, "http://callisto:3210") {
		session.EnableInsecureCookie()
	}

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
		// Dynamic pages: never cache
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
		// not implemented yet
	}

	ref := r.Referer()
	_ = ref // noisy on every request; keep variable for potential future checks
	origin := r.Header.Get("Origin")
	_ = origin // suppress verbose per-request logging

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

			// IMPORTANT: reset reader to the beginning after validation, then read fully
			if seeker, ok := file.(io.Seeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					log.Printf("error seeking uploaded file to start: %v", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
			} else {
				// Reader must be seekable to re-read after validation
				log.Printf("uploaded file is not seekable; cannot re-read for hashing")
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			// Read all bytes exactly once to both save and hash
			avatarData, err := io.ReadAll(file)
			if err != nil {
				log.Printf("error reading uploaded avatar file: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			// Compute SHA-256 hex digest for Filehash (used by ETag)
			sum := sha256.Sum256(avatarData)
			fileHash := hex.EncodeToString(sum[:])

			// Ensure we have a fresh user from DB so reference_id is populated (trigger runs AFTER INSERT)
			freshUser, gerr := db.Storage.GetUserByID(u.ID)
			if gerr != nil {
				log.Printf("error refreshing user before file save: %v", gerr)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if freshUser.ReferenceID == "" {
				log.Printf("user reference_id is empty after refresh; cannot determine data path")
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			// Use the refreshed user for file path computations and persist in session
			u = freshUser
			session.Put(sid, *u)
			session.SyncSessions(sid)

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

			// Build and persist file metadata, including the correct file hash
			fileMeta := &db.File{
				UserID:           u.ID,
				OriginalFilename: fh.Filename,
				Filename:         filepath.Base(avatarPath),
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

		// TODO: Send server event notification about profile update

		log.Printf("user %s updated profile", updatedUser.Email)

		// Redirect to home
		http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// filemanagerUserQuotaHandler
func filemanagerUserQuotaHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := prelude(w, r,
		[]string{
			http.MethodGet, // get user quota
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

	log.Printf("getting file manager quota for user %s", u.Email)

	// TODO: implement quota retrieval and return as JSON or HTML (htmx compatible)

}

// filemanagerHandler serves the file manager interface
func filemanagerHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := prelude(w, r,
		[]string{
			http.MethodGet, // list user files
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

	log.Printf("serving file manager for user %s", u.Email)

	// Preload first page of files to avoid duplicate HTMX initial load
	limit := 20
	offset := 0
	files, err := filemanager.ListFilesByUserID(u.ID, offset, limit)
	if err != nil {
		log.Printf("error listing files for user %s: %v", u.Email, err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	type fileVM struct {
		ID               int64
		OriginalFilename string
		Filename         string
		Filesize         int64
		Filedescription  string
		Filetag          string
		MediaKind        string
		CreatedAt        string
		UserRefID        string
	}
	fileList := make([]fileVM, 0, len(files))
	for _, f := range files {
		fileList = append(fileList, fileVM{
			ID:               f.ID,
			OriginalFilename: f.OriginalFilename,
			Filename:         f.Filename,
			Filesize:         f.Filesize,
			Filedescription:  f.Filedescription,
			Filetag:          f.Filetag,
			MediaKind:        classifyMediaKind(f.Filetype, f.Filename),
			CreatedAt:        f.CreatedAt,
			UserRefID:        u.ReferenceID,
		})
	}
	nextOffset := offset + len(fileList)
	hasMore := len(fileList) == limit

	// serve templates/filemanager.go.tmpl
	data := struct {
		Authed     bool
		User       db.User
		Files      []fileVM
		Limit      int
		NextOffset int
		HasMore    bool
		Error      string
		Message    string
		Config     config.Config
		Q          string
		Sort       string
	}{
		Authed:     true,
		User:       *u,
		Files:      fileList,
		Limit:      limit,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Config:     *config.Cfg,
		Q:          "",
		Sort:       "date_desc",
	}

	err = templates.ExecuteTemplate(w, "filemanager.go.tmpl", data)
	if err != nil {
		log.Printf("template %s execute error: %v", "filemanager.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func filemanagerListHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := prelude(w, r,
		[]string{
			http.MethodGet, // list user files
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

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sort := strings.TrimSpace(r.URL.Query().Get("sort"))
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "html"
	}
	offsetStr := r.URL.Query().Get("offset")
	limitStr := r.URL.Query().Get("limit")

	log.Printf("listing files for user %s offset=%q limit=%q q=%q sort=%q", u.Email, offsetStr, limitStr, q, sort)

	offset := utils.ParseIntWithDefault(offsetStr, 0)
	limit := utils.ParseIntWithDefault(limitStr, 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var files []*db.File
	if q != "" {
		files, err = filemanager.SearchFilesByUserIDFTS(u.ID, q, sort, offset, limit)
	} else {
		files, err = filemanager.ListFilesByUserIDSorted(u.ID, sort, offset, limit)
	}
	if err != nil {
		log.Printf("error listing files for user %s: %v", u.Email, err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if format == "json" {
		// return JSON response
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		b, err := json.MarshalIndent(files, "", "  ")
		if err != nil {
			log.Printf("error writing JSON response: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
		return
	}

	// Prepare lightweight view models including user's reference ID for URL building
	type fileVM struct {
		ID               int64
		OriginalFilename string
		Filename         string
		Filesize         int64
		Filedescription  string
		Filetag          string
		MediaKind        string
		CreatedAt        string
		UserRefID        string
	}
	fileList := make([]fileVM, 0, len(files))
	for _, f := range files {
		fileList = append(fileList, fileVM{
			ID:               f.ID,
			OriginalFilename: f.OriginalFilename,
			Filename:         f.Filename,
			Filesize:         f.Filesize,
			Filedescription:  f.Filedescription,
			Filetag:          f.Filetag,
			MediaKind:        classifyMediaKind(f.Filetype, f.Filename),
			CreatedAt:        f.CreatedAt,
			UserRefID:        u.ReferenceID,
		})
	}

	// default: render HTML fragment (htmx compatible)
	nextOffset := offset + len(fileList)
	hasMore := len(fileList) == limit
	data := struct {
		Authed     bool
		User       db.User
		Files      []fileVM
		Limit      int
		NextOffset int
		HasMore    bool
		Error      string
		Message    string
		Config     config.Config
		Q          string
		Sort       string
	}{
		Authed:     true,
		User:       *u,
		Files:      fileList,
		Limit:      limit,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Config:     *config.Cfg,
		Q:          q,
		Sort:       sort,
	}
	err = templates.ExecuteTemplate(w, "filemanager_list", data)
	if err != nil {
		log.Printf("template %s execute error: %v", "filemanager_list.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}

}

func filemanagerUploadHandler(w http.ResponseWriter, r *http.Request) {
	u, sid, authed, err := prelude(w, r,
		[]string{
			http.MethodGet,  // show upload form
			http.MethodPost, // process file upload
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

	// GET: Show upload form
	if r.Method == http.MethodGet {
		csrf := session.GenerateCSRFToken(w, r)
		data := struct {
			Authed  bool
			User    db.User
			Error   string
			Message string
			Config  config.Config
			Csrf    string
		}{
			Authed: true,
			User:   *u,
			Config: *config.Cfg,
			Csrf:   csrf,
		}
		err := templates.ExecuteTemplate(w, "filemanager_upload.go.tmpl", data)
		if err != nil {
			log.Printf("template error: %v", err)
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	// POST: Process file upload
	if r.Method == http.MethodPost {
		// Parse form with max 500 MB
		err := r.ParseMultipartForm(500 << 20)
		if err != nil {
			// CSRF validation (double-submit cookie)
			if !session.ValidateCSRF(r) {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}

			log.Printf("multipart form parse error: %v", err)
			http.Error(w, "form parse error", http.StatusBadRequest)
			return
		}

		// Get file from form
		file, fh, err := r.FormFile("file")
		if err != nil {
			log.Printf("no file in form: %v", err)
			data := struct {
				Authed  bool
				User    db.User
				Error   string
				Message string
				Config  config.Config
			}{
				Authed: true,
				User:   *u,
				Error:  "Por favor, selecione um arquivo",
				Config: *config.Cfg,
			}
			templates.ExecuteTemplate(w, "filemanager_upload.go.tmpl", data)
			return
		}
		defer file.Close()

		log.Printf("processing uploaded file: %v", fh.Filename)

		// Validate file: accept images, videos, and audio only
		typeDetected, size, err := filemanager.ValidateFile(
			file,
			fh,
			[]string{
				// Images
				"image/jpeg", "image/png", "image/gif", "image/webp",
				// Videos
				"video/mp4", "video/webm", "video/ogg",
				// Audio
				"audio/mpeg", "audio/wav", "audio/ogg", "audio/mp4",
			},
			[]string{
				// Image extensions
				"jpg", "jpeg", "png", "gif", "webp",
				// Video extensions
				"mp4", "webm", "ogv", "ogg",
				// Audio extensions
				"mp3", "wav", "oga", "m4a",
			},
			500<<20, // 500 MB
		)
		if err != nil {
			log.Printf("file validation error: %v", err)
			data := struct {
				Authed  bool
				User    db.User
				Error   string
				Message string
				Config  config.Config
			}{
				Authed: true,
				User:   *u,
				Error:  "Arquivo inválido: " + err.Error(),
				Config: *config.Cfg,
			}
			templates.ExecuteTemplate(w, "filemanager_upload.go.tmpl", data)
			return
		}

		log.Printf("file validated: name %q type=%q size=%d", fh.Filename, typeDetected, size)

		// Reset file pointer after validation
		if seeker, ok := file.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				log.Printf("error seeking file to start: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
		} else {
			log.Printf("uploaded file is not seekable")
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Read file content for hashing
		fileData, err := io.ReadAll(file)
		if err != nil {
			log.Printf("error reading file: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Compute SHA-256 hash
		sum := sha256.Sum256(fileData)
		fileHash := hex.EncodeToString(sum[:])

		// Refresh user to ensure reference_id is populated
		freshUser, gerr := db.Storage.GetUserByID(u.ID)
		if gerr != nil {
			log.Printf("error refreshing user: %v", gerr)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if freshUser.ReferenceID == "" {
			log.Printf("user reference_id is empty")
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		u = freshUser
		session.Put(sid, *u)
		session.SyncSessions(sid)

		// Get user's data directory
		uploadsDir, err := filemanager.DataFilePath(u)
		if err != nil {
			log.Printf("error getting data path: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Generate unique filename and save to disk
		fileExt := strings.ToLower(filepath.Ext(fh.Filename))
		filePath := filepath.Join(uploadsDir, filemanager.FileName()+fileExt)

		log.Printf("saving file to: %q", filePath)
		err = os.WriteFile(filePath, fileData, 0600)
		if err != nil {
			log.Printf("error writing file: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Get form metadata
		description := utils.SanitizeDescription(r.FormValue("description"))
		// Tags CSV (progressive enhancement: accept name="filetag"; fallback to legacy "tag")
		rawTags := strings.TrimSpace(r.FormValue("filetag"))
		if rawTags == "" {
			rawTags = strings.TrimSpace(r.FormValue("tag"))
		}
		tagsCSV, _, nerr := utils.NormalizeTagsCSV(rawTags)
		if nerr != nil {
			data := struct {
				Authed  bool
				User    db.User
				Error   string
				Message string
				Config  config.Config
			}{
				Authed: true,
				User:   *u,
				Error:  "Categorias invalidas: " + nerr.Error(),
				Config: *config.Cfg,
			}
			templates.ExecuteTemplate(w, "filemanager_upload.go.tmpl", data)
			return
		}

		// Create and persist file metadata
		fileMeta := &db.File{
			UserID:           u.ID,
			OriginalFilename: fh.Filename,
			Filename:         filepath.Base(filePath),
			Filesize:         size,
			Filetype:         typeDetected,
			Filehash:         fileHash,
			Filetag:          tagsCSV,
			Filedescription:  description,
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

		log.Printf("file saved: id=%d filename=%s", fileMeta.ID, fileMeta.Filename)

		// Redirect to file manager with success message
		http.Redirect(w, r, config.Cfg.BaseURL+"/files?message=Arquivo+enviado+com+sucesso", http.StatusFound)
	}
}

func filemanagerEditHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := prelude(w, r,
		[]string{http.MethodGet, http.MethodPost},
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

	filename := r.URL.Query().Get("id")
	if filename == "" && r.Method == http.MethodPost {
		filename = r.FormValue("file_id")
	}

	if filename == "" {
		http.Error(w, "file id is required", http.StatusBadRequest)
		return
	}

	// Fetch file - GetFileByFilename returns nil if not found or user doesn't own it
	file, err := db.Storage.GetFileByUserIDAndFilename(u.ID, filename)
	if err != nil {
		log.Printf("error fetching file: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if file == nil {
		http.Error(w, "file not found or access denied", http.StatusNotFound)
		return
	}

	// GET: show edit form
	if r.Method == http.MethodGet {
		csrf := session.GenerateCSRFToken(w, r)
		data := struct {
			Authed    bool
			User      db.User
			File      db.File
			MediaKind string
			Error     string
			Message   string
			Config    config.Config
			Csrf      string
		}{
			Authed:    true,
			User:      *u,
			File:      *file,
			MediaKind: classifyMediaKind(file.Filetype, file.Filename),
			Config:    *config.Cfg,
			Csrf:      csrf,
		}

		err := templates.ExecuteTemplate(w, "filemanager_edit.go.tmpl", data)
		if err != nil {
			log.Printf("template error: %v", err)
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	// POST: save changes
	if r.Method == http.MethodPost {
		// Ensure form is parsed for CSRF and fields
		_ = r.ParseForm()
		if !session.ValidateCSRF(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		description := utils.SanitizeDescription(r.FormValue("description"))

		rawTags := strings.TrimSpace(r.FormValue("filetag"))
		if rawTags == "" {
			rawTags = strings.TrimSpace(r.FormValue("tag"))
		}
		tagsCSV, _, nerr := utils.NormalizeTagsCSV(rawTags)
		if nerr != nil {
			// Re-render page with error
			data := struct {
				Authed    bool
				User      db.User
				File      db.File
				MediaKind string
				Error     string
				Message   string
				Config    config.Config
				Csrf      string
			}{
				Authed:    true,
				User:      *u,
				File:      *file,
				MediaKind: classifyMediaKind(file.Filetype, file.Filename),
				Error:     "Categorias invalidas: " + nerr.Error(),
				Config:    *config.Cfg,
				Csrf:      session.GenerateCSRFToken(w, r),
			}
			templates.ExecuteTemplate(w, "filemanager_edit.go.tmpl", data)
			return
		}

		// Update file metadata via DB layer
		err := db.Storage.UpdateFileMetadataByUserAndFilename(u.ID, filename, description, tagsCSV)

		if err != nil {
			log.Printf("error updating file: %v", err)
			http.Error(w, "error updating file", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, config.Cfg.BaseURL+"/files?message=Arquivo+atualizado+com+sucesso", http.StatusFound)
	}
}

func filemanagerDeleteHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := prelude(w, r,
		[]string{
			http.MethodGet,
			http.MethodPost, // soft delete file (mark as deleted)
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

	filename := r.FormValue("file_id")
	if filename == "" {
		filename = r.URL.Query().Get("file_id")
	}
	if filename == "" {
		filename = r.URL.Query().Get("id")
	}
	if err := filemanager.ValidateFilename(filename); err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	// Ensure the file exists and belongs to the user (and is not already deleted)
	f, err := db.Storage.GetFileByUserIDAndFilename(u.ID, filename)
	if err != nil {
		log.Printf("error fetching file for delete: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if f == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// If this is a POST with confirm=yes, perform the delete; otherwise, show confirmation page
	if r.Method == http.MethodPost && r.FormValue("confirm") == "yes" {
		_ = r.ParseForm()
		if !session.ValidateCSRF(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		// Soft delete: mark as deleted via DB layer
		err = db.Storage.SoftDeleteFileByUserAndFilename(u.ID, filename)
		if err != nil {
			log.Printf("error soft-deleting file: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, config.Cfg.BaseURL+"/files?message="+url.QueryEscape("Arquivo excluido com sucesso"), http.StatusFound)
		return
	}

	// Render confirmation page
	csrf := session.GenerateCSRFToken(w, r)
	data := struct {
		Authed    bool
		User      db.User
		File      db.File
		MediaKind string
		Error     string
		Message   string
		Config    config.Config
		Csrf      string
	}{
		Authed:    true,
		User:      *u,
		File:      *f,
		MediaKind: classifyMediaKind(f.Filetype, f.Filename),
		Config:    *config.Cfg,
		Csrf:      csrf,
	}
	if err := templates.ExecuteTemplate(w, "filemanager_delete_confirm.go.tmpl", data); err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
}

func parseRFC3339(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// classifyMediaKind decides the media kind using MIME type with a fallback to file extension.
// Returns one of: "image", "video", "audio", or "other".
func classifyMediaKind(mimeType, filename string) string {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if strings.HasPrefix(mt, "image/") {
		return "image"
	}
	if strings.HasPrefix(mt, "video/") {
		return "video"
	}
	if strings.HasPrefix(mt, "audio/") {
		return "audio"
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	switch ext {
	// Images
	case "jpg", "jpeg", "png", "gif", "webp":
		return "image"
	// Videos
	case "mp4", "webm", "ogv", "mov", "avi", "mkv", "flv", "wmv":
		return "video"
	// Audio
	case "mp3", "wav", "oga", "m4a", "aac", "ogg":
		return "audio"
	}
	return "other"
}

// serve user files
func fileHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := prelude(w, r,
		[]string{
			http.MethodGet,  // serve file
			http.MethodHead, // ETag and Last-Modified support
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

	log.Printf("method %s file %q for user %s",
		r.Method,
		r.URL.Path,
		u.Email)

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

	if fileMeta == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	if strings.TrimSpace(fileMeta.Filehash) == "" {
		log.Printf("invariant violation: empty filehash for id=%d", fileMeta.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var lastMod time.Time
	t, ok := parseRFC3339(fileMeta.CreatedAt)
	if ok {
		lastMod = t.UTC()
	}
	hasLastMod := !lastMod.IsZero()

	etag := `"` + "sha256:" + fileMeta.Filehash + `"`

	inm := r.Header.Get("If-None-Match")
	if inm != "" {
		matches := func(h, target string) bool {
			h = strings.TrimSpace(h)
			return h == target || strings.TrimPrefix(h, "W/") == target
		}

		parts := strings.SplitSeq(inm, ",")
		for p := range parts {
			if matches(p, etag) || strings.TrimSpace(p) == "*" {
				w.Header().Set("ETag", etag)
				if hasLastMod {
					w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
				}
				w.Header().Set("Cache-Control", "private, max-age=86400")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	ims := r.Header.Get("If-Modified-Since")
	if hasLastMod && ims != "" {
		if t, err := time.Parse(http.TimeFormat, ims); err == nil {
			if !lastMod.After(t) {
				w.Header().Set("ETag", etag)
				w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
				w.Header().Set("Cache-Control", "private, max-age=86400")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	// Common headers
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Accept-Ranges", "bytes")
	if hasLastMod {
		w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
	}

	// For HEAD, avoid touching filesystem; respond using DB metadata only
	if r.Method == http.MethodHead {
		if fileMeta.Filetype != "" {
			w.Header().Set("Content-Type", fileMeta.Filetype)
		}
		if fileMeta.Filesize > 0 {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", fileMeta.Filesize))
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// GET: open and serve the file; using ServeContent to honor DB-derived mod time
	// Resolve absolute path from user's data directory + filename (no filepath field)
	owner, oerr := db.Storage.GetUserByID(fileMeta.UserID)
	if oerr != nil || owner == nil {
		log.Printf("error resolving file owner user id=%d: %v", fileMeta.UserID, oerr)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	ownerPath, perr := filemanager.DataFilePath(owner)
	if perr != nil {
		log.Printf("error resolving owner data path: %v", perr)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	absPath := filepath.Join(ownerPath, fileMeta.Filename)
	f, err := os.Open(absPath)
	if err != nil {
		log.Printf("error opening file for serve: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if fileMeta.Filetype != "" {
		w.Header().Set("Content-Type", fileMeta.Filetype)
	}

	http.ServeContent(w, r, fileMeta.Filename, lastMod, f)
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

	link := config.Cfg.BaseURL + "/link/" + token
	if config.Cfg.BaseURL == "http://localhost:3210" {
		// for local dev, print the link to the console
		log.Printf("debug magic link: %s", link)

		// Return redirect URL
		redirectURL := config.Cfg.BaseURL +
			"/?message=" +
			url.QueryEscape("Magic link (dev mode): "+link)

		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	// send the email with the link
	ret, err := mail.Send(mail.EmailRequest{
		From:    "noreply@" + strings.TrimPrefix(config.Cfg.EmailDomain, "https://"),
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
		return // FIXED: was missing return statement
	}

	// user must be enabled to use SSE
	if !u.Enabled {
		log.Printf("SSE: User %s is not enabled (enabled=%v)", u.Email, u.Enabled)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

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
			//log.Printf("SSE context done for user %s: %v", u.Email, ctx.Err())
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

// Recursively walk an http.FileSystem and build an index keyed by relative path.
// Uses only stdlib http.FileSystem APIs (compatible with embed and dev FS).
func buildAssetsIndex(fs http.FileSystem) map[string]assetMeta {
	index := make(map[string]assetMeta)
	var walk func(string)
	walk = func(dir string) {
		f, err := fs.Open(dir)
		if err != nil {
			log.Printf("assets: open %q error: %v", dir, err)
			return
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			log.Printf("assets: stat %q error: %v", dir, err)
			return
		}

		if fi.IsDir() {
			infos, err := f.Readdir(-1)
			if err != nil {
				log.Printf("assets: readdir %q error: %v", dir, err)
				return
			}
			for _, child := range infos {
				name := child.Name()
				childPath := name
				if dir != "." {
					childPath = dir + "/" + name
				}
				walk(childPath)
			}
			return
		}

		// File: read all once, compute hash and content type
		filePath := dir
		cf, err := fs.Open(filePath)
		if err != nil {
			log.Printf("assets: open file %q error: %v", filePath, err)
			return
		}
		defer cf.Close()

		data, err := io.ReadAll(cf)
		if err != nil {
			log.Printf("assets: read file %q error: %v", filePath, err)
			return
		}

		sum := sha256.Sum256(data)
		etag := `"` + "sha256:" + hex.EncodeToString(sum[:]) + `"`

		ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
		if ctype == "" {
			// Fallback detection
			ctype = http.DetectContentType(data)
		}

		index[filePath] = assetMeta{
			ETag:        etag,
			ModTime:     fi.ModTime().UTC(),
			Size:        int64(len(data)),
			ContentType: ctype,
			Data:        data,
		}
	}

	// Root of http.FileServer(assets.FS) is "." after StripPrefix("/assets/")
	walk(".")
	return index
}

// Sanitize asset path after stripping "/assets/"
func cleanAssetPath(p string) (string, bool) {
	p = strings.TrimPrefix(p, "/")
	p = filepath.ToSlash(p)
	if p == "" || strings.Contains(p, "..") {
		return "", false
	}
	return p, true
}

func assetsHandler(w http.ResponseWriter, r *http.Request) {
	rel, ok := cleanAssetPath(strings.TrimPrefix(r.URL.Path, "/assets/"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	// Directory requests are not served
	if strings.HasSuffix(rel, "/") || rel == "." {
		http.NotFound(w, r)
		return
	}

	meta, ok := assetsIndex[rel]
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Cache policy:
	// - Versioned URLs (e.g., /assets/file.css?v=gitTag) get long-lived immutable caching.
	// - Bare URLs revalidate on each request to ensure freshness in dev.
	if r.URL.RawQuery != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	w.Header().Set("ETag", meta.ETag)
	if !meta.ModTime.IsZero() {
		w.Header().Set("Last-Modified", meta.ModTime.Format(http.TimeFormat))
	}
	w.Header().Set("Accept-Ranges", "bytes")
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	if meta.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", meta.Size))
	}

	// If-None-Match has precedence
	inm := r.Header.Get("If-None-Match")
	if inm != "" {
		parts := strings.SplitSeq(inm, ",")
		for p := range parts {
			p = strings.TrimSpace(p)
			// Match strong or weak by stripping optional W/ prefix from client
			if p == meta.ETag || strings.TrimPrefix(p, "W/") == meta.ETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if p == "*" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	// If-Modified-Since (only if we have a valid mod time)
	if !meta.ModTime.IsZero() {
		if ims := r.Header.Get("If-Modified-Since"); ims != "" {
			if t, err := time.Parse(http.TimeFormat, ims); err == nil {
				if !meta.ModTime.After(t) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
	}

	// HEAD: headers already set
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// GET: serve from memory with correct mod time (supports ranges)
	br := bytes.NewReader(meta.Data)
	http.ServeContent(w, r, filepath.Base(rel), meta.ModTime, br)
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

	// Ensure correct MIME type for web app manifest
	// This helps browsers treat the manifest correctly and aligns with caching.
	// Go's default mime map may not include .webmanifest.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")

	// Precompute assets metadata and content once at startup
	assetsIndex = buildAssetsIndex(assets.FS)

	// Serve embedded assets with aggressive, revalidation-first caching
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			assetsHandler(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
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

	// Debug endpoint to check session state
	mux.HandleFunc("/debug/session", func(w http.ResponseWriter, r *http.Request) {
		sid, ok := session.GetCookie(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "No session cookie found\n")
			return
		}

		u, ok := session.Get(sid)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Session not found in store: %s\n", sid[:min(8, len(sid))])
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "Session found!\n")
		fmt.Fprintf(w, "Email: %s\n", u.Email)
		fmt.Fprintf(w, "Enabled: %v\n", u.Enabled)
		fmt.Fprintf(w, "ID: %d\n", u.ID)
	})

	mux.HandleFunc("/events", sseHandler) // server-sent events)
	mux.HandleFunc("/logout", logoutHandler)
	mux.HandleFunc("/me", meHandler) // user profile

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
	mux.HandleFunc("/files/", filemanagerHandler)
	mux.HandleFunc("/files/list", filemanagerListHandler)       // list user files
	mux.HandleFunc("/files/quota", filemanagerUserQuotaHandler) // get user quota
	mux.HandleFunc("/files/upload", filemanagerUploadHandler)   // upload user file
	mux.HandleFunc("/files/edit", filemanagerEditHandler)       // edit file metadata
	mux.HandleFunc("/files/delete", filemanagerDeleteHandler)   // delete user file

	forum.Routers(mux) // forum routes

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

	log.Println("Shutting down gracefully (no context timeout)...")
	// Disable keep-alives to encourage clients to disconnect quickly.
	srv.SetKeepAlivesEnabled(false)

	// Notify SSE clients (best-effort); they will see disconnect soon after.
	n := session.BroadcastSSENotification("shutdown")
	log.Printf("Broadcasted shutdown to %d SSE channels", n)
	// Small pause to allow kernel buffers to flush messages.
	time.Sleep(250 * time.Millisecond)

	// Save sessions to file
	err = session.SaveToGobFile("sessions.gob")
	if err != nil {
		log.Printf("Session save error: %v", err)
	}

	// Direct close without waiting for a context deadline.
	if cerr := srv.Close(); cerr != nil {
		log.Printf("Server close error: %v", cerr)
	}

	if db.Storage != nil {
		db.Storage.Close()
	}
	log.Println("Server stopped.")
}
