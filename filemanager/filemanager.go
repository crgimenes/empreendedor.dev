package filemanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"edev/config"
	"edev/db"
	"edev/log"
	"edev/utils"
)

func SaveFileMetadata(f *db.File) (*db.File, error) {
	return db.Storage.SaveFileMetadata(f)
}

func GetFileByUserIDAndFilename(userID int64, filename string) (*db.File, error) {
	return db.Storage.GetFileByUserIDAndFilename(userID, filename)
}

func GetFileByUserReferenceIDAndFilename(
	userRefID string,
	filename string,
) (*db.File, error) {
	return db.Storage.GetFileByUserReferenceIDAndFilename(userRefID, filename)
}

// ensureDir creates the given directory path if it does not exist.
// It behaves like "mkdir -p", creating all necessary parent directories.
func ensureDir(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err == nil {
		if info.IsDir() {
			return absPath, nil
		}
		return "", os.ErrExist
	}

	if os.IsNotExist(err) {
		err = os.MkdirAll(absPath, 0o700)
		if err != nil {
			return "", err
		}
		return absPath, nil
	}

	return "", err
}

// UploadPath returns the upload directory path for the given user.
// It ensures that the directory exists, creating it if necessary.
func UploadPath(u db.User) (string, error) {
	wd := config.Cfg.UploadPath

	path := filepath.Join(
		wd,
		"uploads",
		u.ReferenceID,
		"temp")

	absPath, err := ensureDir(path)
	if err != nil {
		return "", err
	}

	return absPath, nil
}

var (
	ErrorInvalidFileType = errors.New("invalid file type")
	ErrorFileTooLarge    = errors.New("file size exceeds the maximum allowed size")
	ErrorFileRead        = errors.New("error reading file")
	ErrorFileExtension   = errors.New("invalid file extension")
	ErrorFileNameInvalid = errors.New("invalid file name")
	ErrorEmptyFile       = errors.New("file is empty") // new
)

const (
	mimeBufSize       = 512
	maxFileNameLength = 255
)

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
	// Basic filename checks
	if fh.Filename == "" {
		log.Println("Empty filename")
		return "", 0, ErrorFileNameInvalid
	}
	if len(fh.Filename) > maxFileNameLength {
		log.Println("File name too long:", len(fh.Filename), ">", maxFileNameLength)
		return "", 0, ErrorFileNameInvalid
	}
	// Prevent path traversal or directory components
	if filepath.Base(fh.Filename) != fh.Filename {
		log.Println("Path traversal attempt in filename:", fh.Filename)
		return "", 0, ErrorFileNameInvalid
	}
	// Invalid characters (defense-in-depth)
	invalidChars := []rune{'/', '\\', '<', '>', ':', '"', '|', '?', '*'}
	for _, char := range invalidChars {
		if strings.ContainsRune(fh.Filename, char) {
			log.Println("Invalid character in file name:", string(char))
			return "", 0, ErrorFileNameInvalid
		}
	}

	// Normalize and validate extension (case-insensitive, allow with/without dot in input list)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fh.Filename), "."))
	normExts := make([]string, 0, len(acceptedExtensions))
	for _, e := range acceptedExtensions {
		normExts = append(normExts, strings.ToLower(strings.TrimPrefix(e, ".")))
	}
	if !slices.Contains(normExts, ext) {
		log.Println("Invalid file extension:", ext)
		return "", 0, ErrorFileExtension
	}

	// TODO: Implement disk quota per user

	// Check size bounds
	size = fh.Size
	if size > maxSize {
		log.Println("File size exceeds limit:", size, ">", maxSize)
		return "", size, ErrorFileTooLarge
	}
	if size == 0 {
		log.Println("Empty file rejected")
		return "", 0, ErrorEmptyFile
	}

	// Read up to mimeBufSize bytes for content sniffing
	buf := make([]byte, mimeBufSize)
	n, rerr := file.Read(buf)
	if rerr != nil && rerr != io.EOF {
		log.Println("Error reading file for type detection:", rerr)
		return "", size, ErrorFileRead
	}
	if n == 0 {
		log.Println("No data read from file for type detection")
		return "", size, ErrorFileRead
	}
	buf = buf[:n]

	// Reset file pointer
	if _, err = file.Seek(0, 0); err != nil {
		log.Println("Error resetting file pointer:", err)
		return "", size, ErrorFileRead
	}

	// Detect MIME type (sniff) and normalize common aliases
	sniffed := http.DetectContentType(buf)
	sniffed = strings.ToLower(sniffed)
	// Canonicalize a few common audio aliases
	switch sniffed {
	case "audio/x-wav", "audio/wave", "audio/vnd.wave":
		sniffed = "audio/wav"
	case "audio/mp3", "audio/x-mp3", "audio/mpeg3":
		sniffed = "audio/mpeg"
	}

	// Accept if sniffed matches the accepted list
	if slices.Contains(acceptedTypes, sniffed) {
		return sniffed, size, nil
	}

	// If sniffing fell back to generic type, use extension mapping as a safe fallback
	if sniffed == "application/octet-stream" || sniffed == "binary/octet-stream" || sniffed == "text/plain; charset=utf-8" {
		// Map known extensions to canonical MIME types, but only for extensions explicitly allowed
		extToMIME := map[string]string{
			// images
			"jpg":  "image/jpeg",
			"jpeg": "image/jpeg",
			"png":  "image/png",
			"gif":  "image/gif",
			"webp": "image/webp",
			// videos
			"mp4":  "video/mp4",
			"webm": "video/webm",
			"ogv":  "video/ogg",
			// audio
			"mp3": "audio/mpeg",
			"wav": "audio/wav",
			"oga": "audio/ogg",
			"ogg": "audio/ogg",
			"m4a": "audio/mp4",
		}
		if m, ok := extToMIME[ext]; ok && slices.Contains(acceptedTypes, m) {
			return m, size, nil
		}
	}

	// As a last attempt, check if removing vendor prefix (x-) would match
	if strings.Contains(sniffed, "/x-") {
		canonical := strings.Replace(sniffed, "/x-", "/", 1)
		if slices.Contains(acceptedTypes, canonical) {
			return canonical, size, nil
		}
	}

	log.Println("Invalid file type detected:", sniffed)
	return sniffed, size, ErrorInvalidFileType
}

func ValidateFilename(filename string) error {
	if filename == "" {
		return ErrorFileNameInvalid
	}
	if len(filename) > maxFileNameLength {
		return ErrorFileNameInvalid
	}
	// Prevent path traversal or directory components
	if filepath.Base(filename) != filename {
		return ErrorFileNameInvalid
	}
	// Invalid characters (defense-in-depth)
	invalidChars := []rune{'/', '\\', '<', '>', ':', '"', '|', '?', '*'}
	for _, char := range invalidChars {
		if strings.ContainsRune(filename, char) {
			return ErrorFileNameInvalid
		}
	}
	return nil
}

// DataFilePath returns the directory path for storing processed data files for a given user.
// It ensures that the directory exists, creating it if necessary.
func DataFilePath(u *db.User) (string, error) {
	if u == nil || u.ReferenceID == "" {
		return "", errors.New("user reference ID is required")
	}
	wd := config.Cfg.DataPath

	// absolute path
	if strings.HasPrefix(wd, "./") {
		absWd, err := filepath.Abs(wd)
		if err != nil {
			return "", err
		}
		wd = absWd
	}

	if len(u.ReferenceID) < 16 {
		return "", errors.New("invalid user reference ID")
	}

	// shardes by first 2 letter of user reference ID
	s1 := fmt.Sprintf("%c%c", u.ReferenceID[0], u.ReferenceID[1])
	s2 := fmt.Sprintf("%c%c", u.ReferenceID[2], u.ReferenceID[3])

	path := filepath.Join(
		wd,
		"uploads",
		"users",
		s1,
		s2,
		u.ReferenceID,
	)

	absPath, err := ensureDir(path)
	if err != nil {
		return "", err
	}

	return absPath, nil
}

// MakeRelativeToDataPath converts an absolute file path under Config.DataPath
// into a path relative to Config.DataPath. If the input path is already
// relative, it is returned unchanged. If the path is absolute but does not
// reside under Config.DataPath, the original path is returned.
func MakeRelativeToDataPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	// Already relative
	if !filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	base := config.Cfg.DataPath
	// Normalize base to absolute if needed
	if strings.HasPrefix(base, "./") {
		absBase, err := filepath.Abs(base)
		if err != nil {
			return "", err
		}
		base = absBase
	}
	// Compute relative path
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return "", err
	}
	// If rel starts with "..", p is not under base; keep original absolute
	if strings.HasPrefix(rel, "..") {
		return filepath.Clean(p), nil
	}
	return filepath.Clean(rel), nil
}

// ResolveAbsoluteFromDataPath returns an absolute path for a stored file path.
// If the provided path is absolute, it is returned as-is. If it is relative,
// it will be joined with Config.DataPath and cleaned.
func ResolveAbsoluteFromDataPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	base := config.Cfg.DataPath
	if strings.HasPrefix(base, "./") {
		absBase, err := filepath.Abs(base)
		if err != nil {
			return "", err
		}
		base = absBase
	}
	return filepath.Clean(filepath.Join(base, p)), nil
}

// FileName generates random file name
func FileName() string {
	return utils.NewOpaqueIDShort()
}

// FileHash computes the SHA-256 hash of the given multipart file.
// It reads the file content and returns the hexadecimal representation of the hash.
func FileHash(file multipart.File) (string, error) {
	_, err := file.Seek(0, io.SeekStart) // reset position
	if err != nil {
		return "", err
	}

	hash := sha256.New()

	// Copy file content directly to the hasher
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	sum := hash.Sum(nil)
	return hex.EncodeToString(sum), nil
}

func ListFilesByUserID(userID int64, offset, limit int) ([]*db.File, error) {
	return db.Storage.ListFilesByUserID(userID, offset, limit)
}

func SearchFilesByUserIDFTS(userID int64, query, sort string, offset, limit int) ([]*db.File, error) {
	return db.Storage.SearchFilesByUserIDFTS(userID, query, sort, offset, limit)
}

func ListFilesByUserIDSorted(userID int64, sort string, offset, limit int) ([]*db.File, error) {
	return db.Storage.ListFilesByUserIDSorted(userID, sort, offset, limit)
}
