package config

type Config struct {
	Addrs              string
	BaseURL            string
	DatabaseURL        string
	FakeOAuthBaseURL   string
	FakeOAuthClientID  string
	FakeOAuthEnabled   bool
	FakeOAuthRedirect  string
	GitHubClientID     string
	GitHubClientSecret string
	GitTag             string
	XClientID          string
	XClientSecret      string
}

var Cfg = &Config{
	Addrs:   ":3210",
	BaseURL: "https://empreendedor.dev",
	GitTag:  "dev",

	FakeOAuthRedirect: "/fake/oauth/callback",
	FakeOAuthBaseURL:  "http://127.0.0.1:9100",
	FakeOAuthClientID: "fake-client-id",
}
