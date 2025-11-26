package config

import "time"

type Config struct {
	Addrs               string
	BaseURL             string
	DBFile              string
	DataPath            string //  procesed files storage path
	DiscordClientID     string
	DiscordClientSecret string
	DiscordOAuthEnabled bool
	EmailDomain         string
	FakeOAuthBaseURL    string
	FakeOAuthClientID   string
	FakeOAuthEnabled    bool
	FakeOAuthRedirect   string
	GitHubClientID      string
	GitHubClientSecret  string
	GitTag              string
	GithubOAuthEnabled  bool
	ResendAPIKey        string
	SessionDuration     time.Duration
	SiteDescription     string
	SiteTitle           string
	UploadPath          string // file upload storage path (temporary before processing)
	XClientID           string
	XClientSecret       string
	XOAuthEnabled       bool
}

var Cfg = &Config{
	Addrs:           ":3210",
	BaseURL:         "http://localhost:3210",
	SiteTitle:       "edev",
	SiteDescription: "edev",
	GitTag:          "dev",
	DBFile:          "edev.db",
	SessionDuration: 10 * 24 * time.Hour, // 10 days
	UploadPath:      "./uploads",
	DataPath:        "./data",

	FakeOAuthEnabled:  false,
	FakeOAuthRedirect: "/fake/oauth/callback",
	FakeOAuthBaseURL:  "http://127.0.0.1:9100",
	FakeOAuthClientID: "fake-client-id",
}
