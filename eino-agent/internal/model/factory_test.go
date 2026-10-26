package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
)

func TestOpenAICompatibleGenerate(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "test-model" {
			t.Errorf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"fake answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer srv.Close()

	for _, provider := range []string{config.ProviderOpenAI, config.ProviderArk} {
		t.Run(provider, func(t *testing.T) {
			cm, err := New(context.Background(), &config.Config{
				Provider: provider,
				Model:    "test-model",
				BaseURL:  srv.URL,
				APIKey:   "test-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			msg, err := cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != "fake answer" {
				t.Fatalf("content = %q", msg.Content)
			}
			if gotPath != "/chat/completions" {
				t.Fatalf("path = %q", gotPath)
			}
			if gotAuth != "Bearer test-key" {
				t.Fatalf("authorization = %q", gotAuth)
			}
		})
	}
}

func TestNewRejectsUnknownProvider(t *testing.T) {
	_, err := New(context.Background(), &config.Config{Provider: "nope", Model: "m", APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("error = %v", err)
	}
}
