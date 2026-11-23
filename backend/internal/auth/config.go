package auth

import "os"

// Config holds all authentication settings, loaded from environment variables.
type Config struct {
	Enabled       bool   // master switch for authentication
	LocalEnabled  bool   // allow username/password (local) login
	CookieSecure  bool   // set the Secure flag on the session cookie
	AdminUsername string // username for the bootstrapped admin account
	AdminPassword string // password for the bootstrapped admin account
	PublicURL     string // public base URL of this instance
	// RegistrationMode is the initial registration mode (env REGISTRATION_MODE).
	// It is written to the settings table once at startup; afterwards the
	// database value wins.
	RegistrationMode string
	// TrustProxy makes the rate limiter read the client IP from the first
	// X-Forwarded-For hop. Enable it only behind a reverse proxy that
	// overwrites the header.
	TrustProxy bool
}

// LoadConfig reads auth settings from the environment.
func LoadConfig() Config {
	return Config{
		Enabled:          envBool("AUTH_ENABLED", true),
		LocalEnabled:     envBool("AUTH_LOCAL_ENABLED", true),
		CookieSecure:     envBool("COOKIE_SECURE", false),
		AdminUsername:    envStr("ADMIN_USERNAME", "admin"),
		AdminPassword:    os.Getenv("ADMIN_PASSWORD"),
		PublicURL:        envStr("PUBLIC_URL", "http://localhost:8080"),
		RegistrationMode: envStr("REGISTRATION_MODE", RegistrationModeAdminOnly),
		TrustProxy:       envBool("TRUST_PROXY", false),
	}
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	case "0", "false", "FALSE", "False", "no", "off":
		return false
	}
	return def
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
