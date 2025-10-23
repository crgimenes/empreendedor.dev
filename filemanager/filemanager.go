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

	// Detect MIME type
	typeDetected = http.DetectContentType(buf)
	if !slices.Contains(acceptedTypes, typeDetected) {
		log.Println("Invalid file type detected:", typeDetected)
		return typeDetected, size, ErrorInvalidFileType
	}

	return typeDetected, size, nil
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
