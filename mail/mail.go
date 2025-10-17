package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"edev/config"
)

type EmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	// Optional: "html", "cc", "bcc", "reply_to", "attachments", etc.
}

type EmailResponse struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

/*
Usage example:
	id, err := send(EmailRequest{
		From:    "noreplay@magacorp.com",
		To:      []string{"Jonh Doe <jonhd@example.com>"},
		Subject: "Test from Go",
		Text:    "Hello,\nThis is a test.\n--\nMagacorp",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "send error: %v\n", err)
		return
	}
	fmt.Printf("sent ok, id=%s\n", id)
*/

func Send(req EmailRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := "https://api.resend.com/emails"

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	httpReq.Header.Set("Authorization", "Bearer "+config.Cfg.ResendAPIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		var er EmailResponse
		err = json.Unmarshal(data, &er)
		if err != nil {
			return "", fmt.Errorf("decode: %w", err)
		}
		return er.ID, nil
	}

	return "", fmt.Errorf("resend http %d: %s", resp.StatusCode, string(data))
}

// CanonicalizeEmail returns (canonical, error).
// Policy: we enforce case-insensitive matching globally -> lower(local + domain).
// We also strip Unicode control-format characters (e.g., zero-width) and trailing dot in domain.
//
// We shouldn't interfere with email and shouldn't even require lowercase letters
// like we're doing in this system. However, this is a business decision that may
// impact older systems that are still case-sensitive.
func CanonicalizeEmail(input string) (string, error) {
	s := strings.TrimSpace(input)

	// Parse RFC 5322 to extract the bare address (handles: "Name <user@ex.com>")
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return "", fmt.Errorf("invalid email syntax: %w", err)
	}
	original := addr.Address

	// Remove invisible Unicode "format" chars (Cf) inside the address (defensive)
	var b strings.Builder
	for _, r := range original {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	clean := b.String()

	// Split local@domain
	at := strings.LastIndexByte(clean, '@')
	if at <= 0 || at == len(clean)-1 {
		return "", fmt.Errorf("invalid email address")
	}
	local := clean[:at]
	domain := clean[at+1:]

	// Domain canonicalization
	//    - lower case
	//    - strip a trailing dot (common copy/paste FQDNs)
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	local = strings.ToLower(local)

	// Reject obviously suspicious cases (e.g., empty domain after cleaning)
	if domain == "" || local == "" {
		return "", fmt.Errorf("empty local or domain")
	}

	canonical := local + "@" + domain
	return canonical, nil
}
