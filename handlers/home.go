package handlers

import (
	"io"
	"mime/multipart"
	"net/http"

	"edev/auth"
	"edev/config"
	"edev/db"
	"edev/session"
)

type TemplateExecutor func(io.Writer, string, any) error

type FileValidator func(multipart.File, *multipart.FileHeader, []string, []string, int64) (string, int64, error)

type FileUtilities struct {
	Validate     FileValidator
	DataPath     func(*db.User) (string, error)
	SaveMetadata func(*db.File) (*db.File, error)
	NewFilename  func() string
}

type Dependencies struct {
	Config        *config.Config
	Templates     TemplateExecutor
	FileUtilities FileUtilities
}

type Handlers struct {
	cfg       *config.Config
	templates TemplateExecutor
	files     FileUtilities
}

func New(deps Dependencies) *Handlers {
	if deps.Config == nil {
		deps.Config = config.Cfg
	}

	return &Handlers{
		cfg:       deps.Config,
		templates: deps.Templates,
		files:     deps.FileUtilities,
	}
}

func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,
			http.MethodHead,
		},
		false, // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "0")
		return
	}

	sid, ok := session.GetCookie(r)
	u := db.User{}
	authed := false
	if ok {
		got, ok := session.Get(sid)
		if ok {
			u, authed = got, true
		}
	}

	message := r.URL.Query().Get("message")
	if len(message) > 200 {
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
		Config:  *h.cfg,
	}

	templateName := "index.go.tmpl"
	if authed {
		templateName = "dashboard.go.tmpl"
	}

	err = h.templates(w, templateName, data)
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
