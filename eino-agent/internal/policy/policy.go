// Package policy contains small, explicit safety defaults: sensitive-file
// reads and environment scrubbing for bash. It is not a sandbox; it reduces
// accidental secret exposure from a model that is curious about .env.
package policy

import (
	"path/filepath"
	"strings"
)

var sensitiveNames = map[string]struct{}{
	".env": {}, ".env.local": {}, ".env.production": {}, ".env.development": {},
	"id_rsa": {}, "id_dsa": {}, "id_ecdsa": {}, "id_ed25519": {},
	"credentials": {}, "credentials.json": {}, "secrets.json": {},
	".npmrc": {}, ".pypirc": {}, ".netrc": {}, ".git-credentials": {},
}

var sensitiveExts = map[string]struct{}{
	".pem": {}, ".key": {}, ".p12": {}, ".pfx": {}, ".jks": {}, ".keystore": {}, ".kdbx": {},
}

// IsSensitive reports whether a workspace-relative path looks like a secret.
func IsSensitive(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := strings.ToLower(filepath.Base(rel))
	if _, ok := sensitiveNames[base]; ok {
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	if _, ok := sensitiveExts[strings.ToLower(filepath.Ext(base))]; ok {
		return true
	}
	lower := strings.ToLower(rel)
	for _, dir := range []string{".ssh/", ".aws/", ".gnupg/", ".kube/"} {
		if strings.Contains(lower, dir) {
			return true
		}
	}
	return false
}

// SanitizedEnv returns environ with secret-looking variables removed unless
// allowAll is true. PATH, HOME, shell settings and toolchain variables survive.
func SanitizedEnv(environ []string, allowAll bool) []string {
	if allowAll {
		return append([]string(nil), environ...)
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if IsSecretEnv(k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// IsSecretEnv reports whether an environment variable name looks secret.
func IsSecretEnv(key string) bool {
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, "AWS_") {
		return true
	}
	for _, marker := range []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
