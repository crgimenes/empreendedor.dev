package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type EmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

type EmailResponse struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

var apiKey string

func Setup(resendAPIKey string) { apiKey = resendAPIKey }

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

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
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

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var er EmailResponse
		err = json.Unmarshal(data, &er)
		if err != nil {
			return "", fmt.Errorf("decode: %w", err)
		}
		return er.ID, nil
	}

	return "", fmt.Errorf("resend http %d: %s", resp.StatusCode, string(data))
}
