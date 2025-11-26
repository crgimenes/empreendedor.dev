package filemanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"edev/auth"
	"edev/config"
	"edev/db"
	"edev/log"
	"edev/session"
	"edev/templates"
	"edev/utils"
)

// Routes registers all filemanager-related routes with the provided ServeMux.
func Routes(mux *http.ServeMux) {
	mux.HandleFunc("/file/", serveFileHandler)
	mux.HandleFunc("/files/", indexHandler)
	mux.HandleFunc("/files/list", listHandler)     // list user files
	mux.HandleFunc("/files/quota", quotaHandler)   // get user quota
	mux.HandleFunc("/files/upload", uploadHandler) // upload user file
	mux.HandleFunc("/files/edit", editHandler)     // edit file metadata
	mux.HandleFunc("/files/delete", deleteHandler) // delete user file
}

// quotaHandler
func quotaHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet, // get user quota
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	log.Printf("getting file manager quota for user %s", u.Email)

	// TODO: implement quota retrieval and return as JSON or HTML (htmx compatible)

}

// indexHandler serves the file manager interface
func indexHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet, // list user files
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	log.Printf("serving file manager for user %s", u.Email)

	// Preload first page of files to avoid duplicate HTMX initial load
	limit := 20
	offset := 0
	files, err := ListFilesByUserID(u.ID, offset, limit)
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
			MediaKind:        ClassifyMediaKind(f.Filetype, f.Filename),
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

func listHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet, // list user files
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
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
		files, err = SearchFilesByUserIDFTS(u.ID, q, sort, offset, limit)
	} else {
		files, err = ListFilesByUserIDSorted(u.ID, sort, offset, limit)
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
			MediaKind:        ClassifyMediaKind(f.Filetype, f.Filename),
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

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	u, sid, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,  // show upload form
			http.MethodPost, // process file upload
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
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
		typeDetected, size, err := ValidateFile(
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
		uploadsDir, err := DataFilePath(u)
		if err != nil {
			log.Printf("error getting data path: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Generate unique filename and save to disk
		fileExt := strings.ToLower(filepath.Ext(fh.Filename))
		filePath := filepath.Join(uploadsDir, FileName()+fileExt)

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

		fileMeta, err = SaveFileMetadata(fileMeta)
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

func editHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet, http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
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
			MediaKind: ClassifyMediaKind(file.Filetype, file.Filename),
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
				MediaKind: ClassifyMediaKind(file.Filetype, file.Filename),
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

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,
			http.MethodPost, // soft delete file (mark as deleted)
		},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	filename := r.FormValue("file_id")
	if filename == "" {
		filename = r.URL.Query().Get("file_id")
	}
	if filename == "" {
		filename = r.URL.Query().Get("id")
	}
	if err := ValidateFilename(filename); err != nil {
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
		MediaKind: ClassifyMediaKind(f.Filetype, f.Filename),
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

// ClassifyMediaKind decides the media kind using MIME type with a fallback to file extension.
// Returns one of: "image", "video", "audio", or "other".
func ClassifyMediaKind(mimeType, filename string) string {
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
func serveFileHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{
			http.MethodGet,  // serve file
			http.MethodHead, // ETag and Last-Modified support
		},
		true,  // check auth
		false, // check ratelimit
		false, // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
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

	err = ValidateFilename(filename)
	if err != nil {
		log.Printf("invalid filename: %v", err)
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	fileMeta, err := GetFileByUserReferenceIDAndFilename(userRefID, filename)
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
	ownerPath, perr := DataFilePath(owner)
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
