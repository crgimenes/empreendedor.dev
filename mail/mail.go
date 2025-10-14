package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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
