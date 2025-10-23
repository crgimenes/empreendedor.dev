package main

import (
	"context"
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
		"img-src 'self' data: https: *.githubusercontent.com github.com *.twimg.com pbs.twimg.com",
		"style-src 'self' 'unsafe-inline'",
		"frame-ancestors 'none'",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", csp)

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

	sid, ok := session.GetCookie(r)
	u := db.User{}
	authed := false
	if ok {
		if got, ok := session.Get(sid); ok {
			u, authed = got, true
		}
	}

	// Check for message in query parameter
	message := r.URL.Query().Get("message")

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
	gitHubProvider = GitHubProvider{}
	xProvider      = XProvider{}
	fakeProvider   = FakeProvider{}
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
	log.Printf("referer: %s", ref)

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

		username := r.FormValue("username")
		avatarURL := r.FormValue("avatar_url")

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

		/*
		   // ValidateFile performs comprehensive validation on an uploaded multipart file.
		   // It checks the file name for length and invalid characters, validates the file extension
		   // against a whitelist, ensures the file size doesn't exceed the maximum limit, and
		   // verifies the MIME type by reading the file content.
		   //
		   // Parameters:
		   //   - file: The multipart.File to validate
		   //   - fh: The multipart.FileHeader containing file metadata
		   //   - acceptedTypes: Slice of accepted MIME types (e.g., "image/jpeg", "text/plain")
		   //   - acceptedExtensions: Slice of accepted file extensions without dots (e.g., "jpg", "txt")
		   //   - maxSize: Maximum allowed file size in bytes
		   //
		   // Returns:
		   //   - typeDetected: The detected MIME type of the file
		   //   - size: The size of the file in bytes
		   //   - err: Error if validation fails, nil if successful
		   //
		   // The function will return specific errors for different validation failures:
		   //   - ErrorFileNameInvalid: File name is too long (>255 chars) or contains invalid characters
		   //   - ErrorFileExtension: File extension is not in the accepted list
		   //   - ErrorFileTooLarge: File size exceeds the maximum limit
		   //   - ErrorFileRead: Error occurred while reading the file
		   //   - ErrorInvalidFileType: Detected MIME type is not in the accepted list
		   //
		   // Note: The function reads the first 512 bytes of the file for MIME type detection
		   // and resets the file pointer to the beginning after reading.
		   func ValidateFile(
		   	file multipart.File,
		   	fh *multipart.FileHeader,
		   	acceptedTypes []string, // accepted MIME types
		   	acceptedExtensions []string, // accepted file extensions
		   	maxSize int64, // max file size in bytes
		   ) (typeDetected string, size int64, err error) {

		*/

		if file != nil {
			log.Printf("uploaded avatar file: %v", fh.Filename)

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

			err = os.WriteFile(avatarPath, avatarData, 0600)
			if err != nil {
				log.Printf("error saving uploaded avatar file: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			///////////////////////////////////////////////////////////
			///////////////////////////////////////////////////////////
			///////////////////////////////////////////////////////////
			// TODO: save file on database
			///////////////////////////////////////////////////////////
			///////////////////////////////////////////////////////////
			///////////////////////////////////////////////////////////

			avatarURL = config.Cfg.BaseURL + "/" + avatarPath
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

		log.Printf("user %s updated profile", updatedUser.Email)

		// Redirect to home
		http.Redirect(w, r, config.Cfg.BaseURL+"/", http.StatusFound)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handlerLink(w http.ResponseWriter, r *http.Request) {
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
		false, // check ratelimit
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

	email, err = mail.CanonicalizeEmail(email)
	if err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	// generate a random token
	token := utils.RandomString(16)

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
	mux.HandleFunc("GET /link/{token}", handlerLink) // for email link login and magic link

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

	// filemanager routes (user files, images, etc.)
	//mux.HandleFunc("/files/", filesHandler)

	// ------------------------------------------
	srv := &http.Server{
		Addr:              config.Cfg.Addrs,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
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
