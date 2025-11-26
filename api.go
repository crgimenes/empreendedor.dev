package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/microcosm-cc/bluemonday"

	"edev/auth"
	"edev/filemanager"
	"edev/log"
)

// mdToHTML converts user-provided Markdown to safe HTML.
func mdToHTML(md []byte) []byte {
	// Markdown parser with common extensions
	extensions := parser.CommonExtensions |
		parser.AutoHeadingIDs |
		parser.NoEmptyLineBeforeBlock

	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(md)

	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	unsafeHTML := markdown.Render(doc, renderer)

	policy := bluemonday.UGCPolicy()

	policy = policy.AddTargetBlankToFullyQualifiedLinks(true)

	// Whitelist
	policy.AllowElements("audio", "video", "source")
	policy.AllowAttrs("controls").OnElements("audio", "video")
	policy.AllowAttrs("preload").OnElements("audio", "video")
	policy.AllowAttrs("poster").OnElements("video")
	policy.AllowAttrs("src").OnElements("audio", "video", "source")
	policy.AllowAttrs("type").OnElements("source")
	policy.AllowAttrs("loop", "muted").OnElements("audio", "video")

	safeHTML := policy.SanitizeBytes(unsafeHTML)

	return bytes.TrimSpace(safeHTML)
}

func MarkdownToHTMLHandler(w http.ResponseWriter, r *http.Request) {
	_, _, _, err := auth.Prelude(w, r,
		[]string{http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Limit body size to avoid abuse; adjust as needed
	const maxBody = 512 * 1024 // 512 KB
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	md, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Convert Markdown -> HTML using your existing function
	html := mdToHTML(md)

	// Respond
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

// GetUserImagesHandler returns a JSON list of user's images for the insert image modal
func GetUserImagesHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
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
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Get all images (not paginated, since modal list should be scrollable)
	// TODO: Consider pagination if users have many images
	const limit = 1000
	files, err := filemanager.ListFilesByUserIDSorted(u.ID, "-created", 0, limit)
	if err != nil {
		log.Printf("error listing images: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Filter only images and build response
	type imageInfo struct {
		Filename     string `json:"filename"`
		DisplayName  string `json:"display_name"`
		Description  string `json:"description"`
		Filetype     string `json:"filetype"`
		FileURL      string `json:"file_url"`
		ThumbnailURL string `json:"thumbnail_url"`
		CreatedAt    string `json:"created_at"`
	}

	images := make([]imageInfo, 0)
	for _, f := range files {
		if filemanager.ClassifyMediaKind(f.Filetype, f.Filename) != "image" {
			continue
		}

		// Build URLs
		fileURL := "/file/" + u.ReferenceID + "/" + f.Filename
		// For now, thumbnail is the same as file (can be optimized later)
		thumbnailURL := fileURL

		images = append(images, imageInfo{
			Filename:     f.Filename,
			DisplayName:  f.OriginalFilename,
			Description:  f.Filedescription,
			Filetype:     f.Filetype,
			FileURL:      fileURL,
			ThumbnailURL: thumbnailURL,
			CreatedAt:    f.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Reverse to show newest first (since ListFilesByUserIDSorted returns oldest first by default)
	for i, j := 0, len(images)-1; i < j; i, j = i+1, j-1 {
		images[i], images[j] = images[j], images[i]
	}

	err = json.NewEncoder(w).Encode(images)
	if err != nil {
		log.Printf("error encoding JSON response: %v", err)
	}
}

// GetUserVideosHandler returns a JSON list of user's videos for the insert video modal
func GetUserVideosHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
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
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Get all videos (not paginated, since modal list should be scrollable)
	const limit = 1000
	files, err := filemanager.ListFilesByUserIDSorted(u.ID, "-created", 0, limit)
	if err != nil {
		log.Printf("error listing videos: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Filter only videos and build response
	type videoInfo struct {
		Filename    string `json:"filename"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Filetype    string `json:"filetype"`
		FileURL     string `json:"file_url"`
		CreatedAt   string `json:"created_at"`
	}

	videos := make([]videoInfo, 0)
	for _, f := range files {
		if filemanager.ClassifyMediaKind(f.Filetype, f.Filename) != "video" {
			continue
		}

		// Build URLs
		fileURL := "/file/" + u.ReferenceID + "/" + f.Filename

		videos = append(videos, videoInfo{
			Filename:    f.Filename,
			DisplayName: f.OriginalFilename,
			Description: f.Filedescription,
			Filetype:    f.Filetype,
			FileURL:     fileURL,
			CreatedAt:   f.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Reverse to show newest first
	for i, j := 0, len(videos)-1; i < j; i, j = i+1, j-1 {
		videos[i], videos[j] = videos[j], videos[i]
	}

	err = json.NewEncoder(w).Encode(videos)
	if err != nil {
		log.Printf("error encoding JSON response: %v", err)
	}
}

// GetUserAudiosHandler returns a JSON list of user's audio files for the insert audio modal
func GetUserAudiosHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
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
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Get all audio files (not paginated, since modal list should be scrollable)
	const limit = 1000
	files, err := filemanager.ListFilesByUserIDSorted(u.ID, "-created", 0, limit)
	if err != nil {
		log.Printf("error listing audios: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Filter only audio and build response
	type audioInfo struct {
		Filename    string `json:"filename"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Filetype    string `json:"filetype"`
		FileURL     string `json:"file_url"`
		CreatedAt   string `json:"created_at"`
	}

	audios := make([]audioInfo, 0)
	for _, f := range files {
		if filemanager.ClassifyMediaKind(f.Filetype, f.Filename) != "audio" {
			continue
		}

		// Build URLs
		fileURL := "/file/" + u.ReferenceID + "/" + f.Filename

		audios = append(audios, audioInfo{
			Filename:    f.Filename,
			DisplayName: f.OriginalFilename,
			Description: f.Filedescription,
			Filetype:    f.Filetype,
			FileURL:     fileURL,
			CreatedAt:   f.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Reverse to show newest first
	for i, j := 0, len(audios)-1; i < j; i, j = i+1, j-1 {
		audios[i], audios[j] = audios[j], audios[i]
	}

	err = json.NewEncoder(w).Encode(audios)
	if err != nil {
		log.Printf("error encoding JSON response: %v", err)
	}
}

func APIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/markdown/preview", MarkdownToHTMLHandler)
	mux.HandleFunc("/api/images", GetUserImagesHandler)
	mux.HandleFunc("/api/videos", GetUserVideosHandler)
	mux.HandleFunc("/api/audios", GetUserAudiosHandler)
}
