package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"

	"edev/log"
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

// PKCE S256 (Proof Key for Code Exchange)
func MakePKCE() (verifier, challenge string) {
	verifier = b64urlNoPad(randBytes(32))
	sum := sha256.Sum256([]byte(verifier))
	challenge = b64urlNoPad(sum[:])
	return
}
