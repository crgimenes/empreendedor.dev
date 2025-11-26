package static

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"edev/assets"
)

type AssetMeta struct {
	ETag        string
	ModTime     time.Time
	Size        int64
	ContentType string
	Data        []byte
}

var index map[string]AssetMeta

func Init() error {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")

	built, err := buildAssetsIndex(assets.FS)
	if err != nil {
		return fmt.Errorf("build assets index: %w", err)
	}

	index = built
	return nil
}

func Handler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if index == nil {
		http.Error(w, "static assets not initialized", http.StatusInternalServerError)
		return
	}

	if strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r)
		return
	}

	rel, ok := cleanAssetPath(strings.TrimPrefix(r.URL.Path, "/assets/"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	meta, ok := index[rel]
	if !ok {
		http.NotFound(w, r)
		return
	}

	setCacheHeaders(w, r.URL.RawQuery != "")
	setMetaHeaders(w, meta)

	inm := r.Header.Get("If-None-Match")
	if inm != "" {
		matched := matchETag(inm, meta.ETag)
		if matched {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	if !meta.ModTime.IsZero() {
		ims := r.Header.Get("If-Modified-Since")
		if ims != "" {
			modTimeOK := checkModTime(ims, meta.ModTime)
			if modTimeOK {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	reader := bytes.NewReader(meta.Data)
	http.ServeContent(w, r, path.Base(rel), meta.ModTime, reader)
}

func ETag(rel string) (string, bool) {
	meta, ok := index[rel]
	if !ok {
		return "", false
	}
	return meta.ETag, true
}

func ContentType(rel string) (string, bool) {
	meta, ok := index[rel]
	if !ok {
		return "", false
	}
	return meta.ContentType, true
}

func Metadata(rel string) (AssetMeta, bool) {
	meta, ok := index[rel]
	if !ok {
		return AssetMeta{}, false
	}
	return meta, true
}

func setCacheHeaders(w http.ResponseWriter, versioned bool) {
	if versioned {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
}

func setMetaHeaders(w http.ResponseWriter, meta AssetMeta) {
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
}

func matchETag(headerValue, etag string) bool {
	parts := strings.SplitSeq(headerValue, ",")
	for p := range parts {
		p = strings.TrimSpace(p)
		if p == etag || strings.TrimPrefix(p, "W/") == etag {
			return true
		}
		if p == "*" {
			return true
		}
	}
	return false
}

func checkModTime(ims string, modTime time.Time) bool {
	parsedTime, err := time.Parse(http.TimeFormat, ims)
	if err != nil {
		return false
	}
	return !modTime.After(parsedTime)
}

func buildAssetsIndex(fsys http.FileSystem) (map[string]AssetMeta, error) {
	index := make(map[string]AssetMeta)

	var walk func(string) error
	walk = func(dir string) error {
		f, err := fsys.Open(dir)
		if err != nil {
			return fmt.Errorf("open %s: %w", dir, err)
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			return fmt.Errorf("stat %s: %w", dir, err)
		}

		if info.IsDir() {
			entries, err := f.Readdir(-1)
			if err != nil {
				return fmt.Errorf("readdir %s: %w", dir, err)
			}

			for _, entry := range entries {
				name := entry.Name()
				child := name
				if dir != "." {
					child = dir + "/" + name
				}
				err = walk(child)
				if err != nil {
					return err
				}
			}
			return nil
		}

		data, err := io.ReadAll(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", dir, err)
		}

		sum := sha256.Sum256(data)
		etag := `"` + "sha256:" + hex.EncodeToString(sum[:]) + `"`

		ctype := mime.TypeByExtension(strings.ToLower(path.Ext(dir)))
		if ctype == "" {
			ctype = http.DetectContentType(data)
		}

		index[path.Clean(dir)] = AssetMeta{
			ETag:        etag,
			ModTime:     info.ModTime().UTC(),
			Size:        int64(len(data)),
			ContentType: ctype,
			Data:        data,
		}

		return nil
	}

	err := walk(".")
	if err != nil {
		return nil, err
	}

	return index, nil
}

func cleanAssetPath(p string) (string, bool) {
	p = strings.TrimPrefix(p, "/")
	p = path.Clean(p)
	if p == "." || strings.HasPrefix(p, "..") {
		return "", false
	}
	return p, true
}
