package app

import "os"

// Config holds runtime configuration for the plugin.
type Config struct {
	Token string // ALEXA_TOKEN — if non-empty, validated against relay hello auth field
	Port  string // PORT — WebSocket server port; "0" for random
}

func loadConfig() Config {
	return Config{
		Token: getEnv("ALEXA_TOKEN", ""),
		Port:  getEnv("PORT", "0"),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
