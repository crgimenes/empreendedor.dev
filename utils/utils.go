package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"regexp"
	"strconv"
	"strings"

	"edev/log"
)

// compiled once for efficiency
var (
	reOpaqueID      = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	reOpaqueIDShort = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
)

// Closer close descriptor to use with defer.
func Closer(f io.Closer) {
	if f == nil {
		return
	}
	err := f.Close()
	if err != nil {
		log.Println(err)
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return b
}

func b64urlNoPad(b []byte) string {
	return strings.TrimRight(
		base64.URLEncoding.EncodeToString(b), "=")
}

func NewOpaqueID() string {
	return b64urlNoPad(randBytes(32))
}

func NewOpaqueIDShort() string {
	return b64urlNoPad(randBytes(16))
}

// ValidateOpaqueID checks if s has a valid format for NewOpaqueID (32 bytes, base64url without padding) returns true if valid
func ValidateOpaqueID(s string) bool {
	if !reOpaqueID.MatchString(s) {
		return false
	}

	data, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(s)
	if err != nil {
		return false
	}

	return len(data) == 32
}

// ValidateOpaqueIDShort checks if s has a valid format for NewOpaqueIDShort (16 bytes, base64url without padding)
func ValidateOpaqueIDShort(s string) bool {
	if !reOpaqueIDShort.MatchString(s) {
		return false
	}

	data, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(s)
	if err != nil {
		return false
	}

	return len(data) == 16
}

func RandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	for i := range n {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// PKCE S256 (Proof Key for Code Exchange)
func MakePKCE() (verifier, challenge string) {
	verifier = b64urlNoPad(randBytes(32))
	sum := sha256.Sum256([]byte(verifier))
	challenge = b64urlNoPad(sum[:])
	return
}

func ParseIntWithDefault(s string, def int) int {
	if s == "" {
		return def
	}
	var v int
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
