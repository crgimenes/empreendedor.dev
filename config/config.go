package config

type Config struct {
	Addrs              string
	BaseURL            string
	GitHubClientID     string
	GitHubClientSecret string
	GitTag             string
}

var Cfg = &Config{
	Addrs:              ":3210",
	BaseURL:            "https://empreendedor.dev",
	GitHubClientID:     "",
	GitHubClientSecret: "",
	GitTag:             "dev",
}
