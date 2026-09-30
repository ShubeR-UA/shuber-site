package internal

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr           string
	DataFile       string
	MediaDir       string
	StorageBackend string
	R2InternalURL  string
	DataKey        string
	SessionTTL     time.Duration
	CookieSecure   bool
	AppEnv         string
	MaxUploadBytes int64
	AdminLogin     string
	AdminPassword  string
}

func LoadConfig() Config {
	appEnv := env("APP_ENV", "development")
	return Config{
		Addr:           env("ADDR", ":8080"),
		DataFile:       env("DATA_FILE", "shuber.json"),
		MediaDir:       env("MEDIA_DIR", "media"),
		StorageBackend: strings.ToLower(env("STORAGE_BACKEND", "local")),
		R2InternalURL:  strings.TrimRight(env("R2_INTERNAL_URL", "http://r2.internal"), "/"),
		DataKey:        env("DATA_KEY", "data/shuber.json"),
		SessionTTL:     envDuration("SESSION_TTL", 7*24*time.Hour),
		CookieSecure:   envBool("COOKIE_SECURE", strings.EqualFold(appEnv, "production")),
		AppEnv:         appEnv,
		MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 200<<20),
		AdminLogin:     strings.TrimSpace(os.Getenv("SHUBER_ADMIN_LOGIN")),
		AdminPassword:  os.Getenv("SHUBER_ADMIN_PASSWORD"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
func envInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
