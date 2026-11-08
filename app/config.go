package app

import "os"

// Config holds runtime configuration for the plugin.
type Config struct {
	ClientID     string // ALEXA_CLIENT_ID — registered client in SldBltData
	ClientSecret string // ALEXA_CLIENT_SECRET — raw secret for the client
	RelayToken   string // ALEXA_RELAY_TOKEN — static connect token for WS authorizer
	WSURL        string // ALEXA_WS_URL — WebSocket endpoint (wss://...)
}

func loadConfig() Config {
	return Config{
		ClientID:     getEnv("ALEXA_CLIENT_ID", ""),
		ClientSecret: getEnv("ALEXA_CLIENT_SECRET", ""),
		RelayToken:   getEnv("ALEXA_RELAY_TOKEN", ""),
		WSURL:        getEnv("ALEXA_WS_URL", ""),
	}
}

// Unset reports whether no Alexa setting is given at all. The plugin then runs
// disabled, so an install that does not use Alexa still starts. A partial
// configuration is a mistake and still fails.
func (c Config) Unset() bool {
	return c.ClientID == "" && c.ClientSecret == "" && c.RelayToken == "" && c.WSURL == ""
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
