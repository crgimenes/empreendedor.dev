package handlers

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"edev/auth"
	"edev/config"
	"edev/db"
	"edev/mail"
	"edev/session"
	"edev/utils"
)

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,
		},
		false, // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sid, ok := session.GetCookie(r)
	if ok {
		_, ok := session.Get(sid)
		if ok {
			http.Redirect(w, r, h.cfg.BaseURL+"/", http.StatusFound)
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
		Config: *h.cfg,
	}

	err = h.templates(w, "login.go.tmpl", data)
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handlers) LoginMagic(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := auth.Prelude(w, r,
		[]string{
			http.MethodPost,
		},
		false, // check auth
		false, // TODO: check ratelimit to prevent abuse
		true,  // prevent cache
	)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	email := r.FormValue("email")
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}

	if len(email) > 254 {
		http.Error(w, "email too long", http.StatusBadRequest)
		return
	}

	email, err = mail.CanonicalizeEmail(email)
	if err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	token := utils.NewOpaqueID()

	err = db.Storage.StoreMagicLinkToken(token, email, time.Now().UTC().Add(15*time.Minute))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	link := h.cfg.BaseURL + "/link/" + token
	if h.cfg.BaseURL == "http://localhost:3210" {
		redirectURL := link

		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	ret, err := mail.Send(mail.EmailRequest{
		From:    "noreply@" + strings.TrimPrefix(h.cfg.EmailDomain, "https://"),
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

	redirectURL := h.cfg.BaseURL +
		"/?message=" +
		url.QueryEscape("Link de acesso enviado! Verifique seu email.")

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *Handlers) MagicLink(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,
		},
		false, // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
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
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}
	if email == "" {
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}

	var u *db.User

	u, err = db.Storage.GetUserOrCreateByEmail(email)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, *u)
	session.SetCookie(w, sid, h.cfg.SessionDuration)

	if u.Username == "" {
		http.Redirect(w, r, h.cfg.BaseURL+"/me", http.StatusFound)
		return
	}

	http.Redirect(w, r, h.cfg.BaseURL+"/", http.StatusFound)
}
