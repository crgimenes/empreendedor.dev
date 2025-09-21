package config

type Config struct {
	Addrs              string
	BaseURL            string
	GitHubClientID     string
	GitHubClientSecret string
	GitTag             string
	XClientID          string
	XClientSecret      string
	FakeOAuthBaseURL   string
	FakeOAuthClientID  string
	FakeOAuthRedirect  string
	FakeOAuthEnabled   bool
}

var Cfg = &Config{
	Addrs:   ":3210",
	BaseURL: "https://empreendedor.dev",
	GitTag:  "dev",
}
