package config

type Config struct {
	Addrs              string
	BaseURL            string
	FakeOAuthBaseURL   string
	FakeOAuthClientID  string
	FakeOAuthEnabled   bool
	FakeOAuthRedirect  string
	GithubOAuthEnabled bool
	GitHubClientID     string
	GitHubClientSecret string
	GitTag             string
	XOAuthEnabled      bool
	XClientID          string
	XClientSecret      string
	DBFile             string
}

var Cfg = &Config{
	Addrs:   ":3210",
	BaseURL: "https://empreendedor.dev",
	GitTag:  "dev",
	DBFile:  "edev.db",

	FakeOAuthRedirect: "/fake/oauth/callback",
	FakeOAuthBaseURL:  "http://127.0.0.1:9100",
	FakeOAuthClientID: "fake-client-id",
}
