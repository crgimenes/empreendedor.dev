package config

import "time"

type Config struct {
	Addrs              string
	BaseURL            string
	DBFile             string
	FakeOAuthBaseURL   string
	FakeOAuthClientID  string
	FakeOAuthEnabled   bool
	FakeOAuthRedirect  string
	GitHubClientID     string
	GitHubClientSecret string
	GitTag             string
	GithubOAuthEnabled bool
	ResendAPIKey       string
	XClientID          string
	XClientSecret      string
	XOAuthEnabled      bool
	SessionDuration    time.Duration
}

var Cfg = &Config{
	Addrs:           ":3210",
	BaseURL:         "https://empreendedor.dev",
	GitTag:          "dev",
	DBFile:          "edev.db",
	SessionDuration: 10 * 24 * time.Hour, // 10 days

	FakeOAuthRedirect: "/fake/oauth/callback",
	FakeOAuthBaseURL:  "http://127.0.0.1:9100",
	FakeOAuthClientID: "fake-client-id",
}
