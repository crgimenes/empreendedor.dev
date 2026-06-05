package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/crgimenes/devengine/api"
	static "github.com/crgimenes/devengine/assets/static"
	"github.com/crgimenes/devengine/auth"
	"github.com/crgimenes/devengine/config"
	"github.com/crgimenes/devengine/db"
	"github.com/crgimenes/devengine/filemanager"
	"github.com/crgimenes/devengine/handlers"
	"github.com/crgimenes/devengine/log"
	"github.com/crgimenes/devengine/middleware"
	"github.com/crgimenes/devengine/session"
	"github.com/crgimenes/devengine/templates"
	"github.com/crgimenes/filo"

	edevAssets "github.com/crgimenes/empreendedor.dev/assets"
	"github.com/crgimenes/empreendedor.dev/mail"
	"github.com/crgimenes/empreendedor.dev/migrations"
	"github.com/crgimenes/empreendedor.dev/oauthproviders"
	edevTemplates "github.com/crgimenes/empreendedor.dev/templates"
)

var (
	GitTag      = "dev" + time.Now().UTC().Format("-20060102-150405")
	emailDomain string
)

func sendMagicLink(email, link string) error {
	from := "noreply@" + strings.TrimPrefix(strings.TrimPrefix(emailDomain, "https://"), "http://")
	id, err := mail.Send(mail.EmailRequest{
		From:    from,
		To:      []string{email},
		Subject: "Seu link de acesso magico",
		Text: "Clique no link para fazer login:\n\t" +
			link +
			"\n\nEste link expira em 15 minutos.\n--\n",
	})
	if err != nil {
		return err
	}
	log.Printf("sent magic link email to %s, id=%s", email, id)
	return nil
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

func runFiloFile(name string) {
	// Create a new Filo state.
	F := filo.New()
	defer F.Close()

	F.SetGlobal("GitTag", ifEmpty(GitTag, config.Cfg.GitTag))
	F.SetGlobal("BaseURL", ifEmpty(os.Getenv("EDEV_BASE_URL"), config.Cfg.BaseURL))
	F.SetGlobal("Address", ifEmpty(os.Getenv("EDEV_ADDRESS"), config.Cfg.Addrs))

	F.SetGlobal("DiscordOAuthEnabled", os.Getenv("EDEV_DISCORD_OAUTH_ENABLED") == "true")
	F.SetGlobal("DiscordClientID", os.Getenv("EDEV_DISCORD_CLIENT_ID"))
	F.SetGlobal("DiscordClientSecret", os.Getenv("EDEV_DISCORD_CLIENT_SECRET"))

	F.SetGlobal("GithubOAuthEnabled", os.Getenv("EDEV_GITHUB_OAUTH_ENABLED") == "true")
	F.SetGlobal("GitHubClientID", os.Getenv("EDEV_GITHUB_CLIENT_ID"))
	F.SetGlobal("GitHubClientSecret", os.Getenv("EDEV_GITHUB_CLIENT_SECRET"))

	F.SetGlobal("DBFile", ifEmpty(
		os.Getenv("EDEV_DB_FILE"), config.Cfg.DBFile))

	F.SetGlobal("ResendAPIKey", os.Getenv("EDEV_RESEND_API_KEY"))

	F.SetGlobal("EmailDomain", os.Getenv("EDEV_EMAIL_DOMAIN"))

	F.SetGlobal("SiteTitle", ifEmpty(
		os.Getenv("EDEV_SITE_TITLE"), config.Cfg.SiteTitle))
	F.SetGlobal("SiteDescription", ifEmpty(
		os.Getenv("EDEV_SITE_DESCRIPTION"), config.Cfg.SiteDescription))

	// Register custom builtin: getEnv
	// Usage in Filo: (getEnv "ENV_VAR_NAME" "default_value")
	if err := F.RegisterBuiltin("getEnv", func(ctx context.Context, args []filo.Value) (filo.Value, error) {
		if len(args) != 2 {
			return filo.Value{}, fmt.Errorf("getEnv expects 2 arguments: env var name and default value")
		}

		envName, err := args[0].AsString()
		if err != nil {
			return filo.Value{}, fmt.Errorf("getEnv: first argument must be string: %v", err)
		}

		defaultValue, err := args[1].AsString()
		if err != nil {
			return filo.Value{}, fmt.Errorf("getEnv: second argument must be string: %v", err)
		}

		value := os.Getenv(envName)
		if value == "" {
			return filo.VString(defaultValue), nil
		}
		return filo.VString(value), nil
	}); err != nil {
		log.Fatal(err)
	}

	// Register print builtin for debugging
	if err := F.RegisterBuiltin("print", func(ctx context.Context, args []filo.Value) (filo.Value, error) {
		strs := make([]string, len(args))
		for i, arg := range args {
			s, err := arg.AsString()
			if err != nil {
				return filo.Value{}, fmt.Errorf("print: argument %d is not a string: %v", i, err)
			}
			strs[i] = s
		}
		log.Println(strings.Join(strs, " "))
		return filo.Value{}, nil
	}); err != nil {
		log.Fatal(err)
	}

	// Read the Filo file.
	b, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		log.Fatal(err)
	}

	err = F.DoString(string(b))
	if err != nil {
		log.Fatal(err)
	}

	config.Cfg.Addrs = F.MustGetString("Address")
	config.Cfg.BaseURL = F.MustGetString("BaseURL")

	oauthproviders.Setup(oauthproviders.Config{
		Discord: oauthproviders.ProviderCreds{
			Enabled:      F.MustGetBool("DiscordOAuthEnabled"),
			ClientID:     F.MustGetString("DiscordClientID"),
			ClientSecret: F.MustGetString("DiscordClientSecret"),
		},
		GitHub: oauthproviders.ProviderCreds{
			Enabled:      F.MustGetBool("GithubOAuthEnabled"),
			ClientID:     F.MustGetString("GitHubClientID"),
			ClientSecret: F.MustGetString("GitHubClientSecret"),
		},
	})
	config.Cfg.GitTag = F.MustGetString("GitTag")

	config.Cfg.DBFile = F.MustGetString("DBFile")

	mail.Setup(F.MustGetString("ResendAPIKey"))
	emailDomain = F.MustGetString("EmailDomain")

	if config.Cfg.BaseURL == "http://localhost:3210" ||
		strings.HasPrefix(config.Cfg.BaseURL, "http://callisto:3210") {
		session.EnableInsecureCookie()
	}

	config.Cfg.SiteTitle = F.MustGetString("SiteTitle")
	config.Cfg.SiteDescription = F.MustGetString("SiteDescription")

}

func main() {
	config.Cfg.GitTag = GitTag

	const initFilo = "init.filo"

	if !fileExists(initFilo) {
		log.Fatal("init.filo not found")
	}

	runFiloFile(initFilo)

	var err error

	db.Storage, err = db.New()
	if err != nil {
		log.Fatalf("Error on db: %s", err)
	}

	// Configure application-specific migrations before running engine+app migrations
	db.SetAppMigrationsFS(migrations.FS)

	err = db.RunMigration()
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

	// Configure application-specific templates before calling ExecuteTemplate
	templates.SetAppTemplatesFS(edevTemplates.EmbeddedFS)

	// Registrar assets da aplicação antes do Init
	static.RegisterAppFS(edevAssets.FS)
	err = static.Init()
	if err != nil {
		log.Fatalf("Static assets init error: %v", err)
	}

	h := handlers.New(handlers.Dependencies{
		Config:    config.Cfg,
		Templates: templates.ExecuteTemplate,
		FileUtilities: handlers.FileUtilities{
			Validate:     filemanager.ValidateFile,
			DataPath:     filemanager.DataFilePath,
			SaveMetadata: filemanager.SaveFileMetadata,
			NewFilename:  filemanager.FileName,
		},
		MagicLinkSender: sendMagicLink,
	})

	mux := http.NewServeMux()

	// Rotas dos packages devengine
	static.Routes(mux)
	auth.Routes(mux)
	oauthproviders.Routes(mux)
	session.Routes(mux)
	h.Routes(mux)
	filemanager.Routes(mux)
	api.Routes(mux)

	// Rotas específicas da aplicação
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/assets/favicon.ico", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/healthz", healthHandler)

	// Debug broadcast endpoint (for testing only)
	mux.HandleFunc("/msg", func(w http.ResponseWriter, r *http.Request) {
		msg := r.URL.Query().Get("msg")
		if msg == "" {
			msg = "debug"
		}
		n := session.BroadcastSSENotification(msg)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "sent to %d channels\n", n)
	})

	// Hello World endpoint - demonstrates application-specific handler
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check for session - user may or may not be logged in
		sid, ok := session.GetCookie(r)
		u := db.User{}
		authed := false
		if ok {
			got, ok := session.Get(sid)
			if ok {
				u, authed = got, true
			}
		}

		data := struct {
			Authed bool
			User   db.User
			Config config.Config
		}{
			Authed: authed,
			User:   u,
			Config: *config.Cfg,
		}

		err := templates.ExecuteTemplate(w, "hello.go.tmpl", data)
		if err != nil {
			log.Printf("Template error: %v", err)
			http.Error(w, "template error", http.StatusInternalServerError)
		}
	})

	// ------------------------------------------
	srv := &http.Server{
		Addr:              config.Cfg.Addrs,
		Handler:           middleware.SecurityHeaders(mux),
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
