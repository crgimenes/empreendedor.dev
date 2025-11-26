package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"edev/auth"
	"edev/config"
	"edev/db"
	"edev/session"
)

func (h *Handlers) Profile(w http.ResponseWriter, r *http.Request) {
	u, sid, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,
			http.MethodPost,
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return
	}

	if r.Method == http.MethodGet {
		data := struct {
			Authed  bool
			User    db.User
			Error   string
			Message string
			Config  config.Config
		}{
			Authed: true,
			User:   *u,
			Config: *h.cfg,
		}
		err := h.templates(w, "me.go.tmpl", data)
		if err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	err = r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	avatarURL := u.AvatarURL

	file, fh, err := r.FormFile("avatar_file")
	if err != nil && err != http.ErrMissingFile {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer func() {
		if file != nil {
			file.Close()
		}
	}()

	if file != nil {
		if h.files.Validate == nil || h.files.DataPath == nil || h.files.SaveMetadata == nil || h.files.NewFilename == nil {
			http.Error(w, "file utilities not configured", http.StatusInternalServerError)
			return
		}

		typeDetected, size, err := h.files.Validate(
			file,
			fh,
			[]string{"image/jpeg", "image/png", "image/gif", "image/webp"},
			[]string{"jpg", "jpeg", "png", "gif", "webp"},
			5<<20,
		)
		if err != nil {
			http.Error(w, "invalid avatar file: "+err.Error(), http.StatusBadRequest)
			return
		}

		if seeker, ok := file.(io.Seeker); ok {
			_, err = seeker.Seek(0, io.SeekStart)
			if err != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		avatarData, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		sum := sha256.Sum256(avatarData)
		fileHash := hex.EncodeToString(sum[:])

		freshUser, gerr := db.Storage.GetUserByID(u.ID)
		if gerr != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if freshUser.ReferenceID == "" {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		u = freshUser
		session.Put(sid, *u)
		session.SyncSessions(sid)

		uploadsDir, err := h.files.DataPath(u)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		fileExt := strings.ToLower(filepath.Ext(fh.Filename))
		avatarPath := filepath.Join(uploadsDir, h.files.NewFilename()+fileExt)

		err = os.WriteFile(avatarPath, avatarData, 0600)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

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

		fileMeta, err = h.files.SaveMetadata(fileMeta)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		avatarURL = "/file/" + u.ReferenceID + "/" + fileMeta.Filename
	}

	updatedUser, err := db.Storage.UpdateUserProfile(u.ID, username, avatarURL)
	if err != nil {
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
			Config: *h.cfg,
		}
		err = h.templates(w, "me.go.tmpl", data)
		if err != nil {
			return
		}
		return
	}

	session.Put(sid, *updatedUser)
	session.SyncSessions(sid)

	http.Redirect(w, r, h.cfg.BaseURL+"/", http.StatusFound)
}
