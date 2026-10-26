package policy

import "testing"

func TestSensitiveFiles(t *testing.T) {
	for _, path := range []string{".env", ".env.local", "keys/id_rsa", "cert.pem", "config/credentials.json", ".ssh/config"} {
		if !IsSensitive(path) {
			t.Errorf("%s should be sensitive", path)
		}
	}
	for _, path := range []string{"main.go", "README.md", "envoy.yaml", "docs/environment.md"} {
		if IsSensitive(path) {
			t.Errorf("%s should not be sensitive", path)
		}
	}
}

func TestSanitizedEnv(t *testing.T) {
	env := []string{"PATH=/bin", "OPENAI_API_KEY=sk-x", "GITHUB_TOKEN=ghp_x", "HOME=/tmp", "AWS_SECRET_ACCESS_KEY=x", "MY_SECRET=x"}
	got := SanitizedEnv(env, false)
	joined := ""
	for _, kv := range got {
		joined += kv + "\n"
	}
	if len(got) != 2 || joined != "PATH=/bin\nHOME=/tmp\n" {
		t.Fatalf("got %v", got)
	}
	if len(SanitizedEnv(env, true)) != len(env) {
		t.Fatal("allowAll should preserve env")
	}
}
