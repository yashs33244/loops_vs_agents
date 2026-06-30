package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGeminiAPIProvider_Name(t *testing.T) {
	g := NewGeminiAPIProviderKey("k", "")
	if g.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", g.Name(), "gemini")
	}
}

func TestGeminiAPIProvider_DefaultModel(t *testing.T) {
	g := NewGeminiAPIProviderKey("k", "")
	if g.Model != defaultGeminiModel {
		t.Errorf("Model = %q, want %q", g.Model, defaultGeminiModel)
	}
	if got := g.model(Request{}); got != defaultGeminiModel {
		t.Errorf("model() = %q, want %q", got, defaultGeminiModel)
	}
	if got := g.model(Request{Model: "override"}); got != "override" {
		t.Errorf("model(override) = %q, want %q", got, "override")
	}
}

func TestNewGeminiAPIProvider_FromEnv(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "env-key")
		g, err := NewGeminiAPIProvider()
		if err != nil {
			t.Fatalf("NewGeminiAPIProvider() error = %v", err)
		}
		if g.APIKey != "env-key" {
			t.Errorf("APIKey = %q, want %q", g.APIKey, "env-key")
		}
	})
	t.Run("unset", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		if _, err := NewGeminiAPIProvider(); err == nil {
			t.Fatal("NewGeminiAPIProvider() error = nil, want error when key unset")
		}
	})
}

func TestParseGeminiResponse(t *testing.T) {
	okBody := []byte(`{
		"candidates": [{"content": {"parts": [{"text": "{\"answer\":42}"}]}}],
		"usageMetadata": {"promptTokenCount": 11, "candidatesTokenCount": 7, "totalTokenCount": 18}
	}`)

	tests := []struct {
		name      string
		body      []byte
		status    int
		wantText  string
		wantTotal int
		wantErr   bool
	}{
		{
			name:      "happy path with usage",
			body:      okBody,
			status:    http.StatusOK,
			wantText:  `{"answer":42}`,
			wantTotal: 18,
		},
		{
			name:    "api error field",
			body:    []byte(`{"error": {"message": "bad key"}}`),
			status:  http.StatusForbidden,
			wantErr: true,
		},
		{
			name:    "non-200 without error field",
			body:    []byte(`{"candidates": []}`),
			status:  http.StatusInternalServerError,
			wantErr: true,
		},
		{
			name:    "invalid json",
			body:    []byte(`not json`),
			status:  http.StatusOK,
			wantErr: true,
		},
		{
			name:     "empty candidates ok",
			body:     []byte(`{"candidates": []}`),
			status:   http.StatusOK,
			wantText: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGeminiResponse(tt.body, tt.status)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseGeminiResponse() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGeminiResponse() error = %v", err)
			}
			if got.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tt.wantText)
			}
			if got.TotalTokens != tt.wantTotal {
				t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, tt.wantTotal)
			}
		})
	}
}

func TestParseGeminiResponse_AllTokenFields(t *testing.T) {
	body := []byte(`{
		"candidates": [{"content": {"parts": [{"text": "hi"}]}}],
		"usageMetadata": {"promptTokenCount": 3, "candidatesTokenCount": 4, "totalTokenCount": 7}
	}`)
	got, err := parseGeminiResponse(body, http.StatusOK)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got.InputTokens != 3 || got.OutputTokens != 4 || got.TotalTokens != 7 {
		t.Errorf("tokens = in:%d out:%d total:%d, want 3/4/7",
			got.InputTokens, got.OutputTokens, got.TotalTokens)
	}
}

func TestBuildRequestBody(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		b := buildRequestBody(Request{Prompt: "what is 6*7?"})
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("body not valid json: %v", err)
		}
		gen, _ := m["generationConfig"].(map[string]any)
		if gen["responseMimeType"] != "application/json" {
			t.Errorf("responseMimeType = %v, want application/json", gen["responseMimeType"])
		}
		if _, hasSys := m["systemInstruction"]; hasSys {
			t.Errorf("unexpected systemInstruction in minimal request")
		}
		if !strings.Contains(string(b), "what is 6*7?") {
			t.Errorf("body missing prompt text: %s", b)
		}
	})

	t.Run("with system prompt and max tokens", func(t *testing.T) {
		b := buildRequestBody(Request{Prompt: "p", SystemPrompt: "sys", MaxTokens: 256})
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("body not valid json: %v", err)
		}
		if _, hasSys := m["systemInstruction"]; !hasSys {
			t.Errorf("missing systemInstruction")
		}
		gen, _ := m["generationConfig"].(map[string]any)
		if gen["maxOutputTokens"] != float64(256) {
			t.Errorf("maxOutputTokens = %v, want 256", gen["maxOutputTokens"])
		}
	})
}

// TestGeminiAPIProvider_Complete_Local drives Complete against an in-process
// httptest server (no real network). It checks the happy path, request shape,
// and that the API key is sent.
func TestGeminiAPIProvider_Complete_Local(t *testing.T) {
	var gotPath, gotQuery, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"candidates": [{"content": {"parts": [{"text": "{\"answer\":42}"}]}}],
			"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 6, "totalTokenCount": 11}
		}`))
	}))
	defer srv.Close()

	g := &GeminiAPIProvider{APIKey: "secret", Model: "gemini-test", BaseURL: srv.URL}
	resp, err := g.Complete(context.Background(), Request{Prompt: "what is 6*7?"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Text != `{"answer":42}` {
		t.Errorf("Text = %q, want %q", resp.Text, `{"answer":42}`)
	}
	if resp.TotalTokens != 11 {
		t.Errorf("TotalTokens = %d, want 11", resp.TotalTokens)
	}
	if !strings.Contains(gotPath, "gemini-test:generateContent") {
		t.Errorf("path = %q, want it to contain model:generateContent", gotPath)
	}
	if !strings.Contains(gotQuery, "key=secret") {
		t.Errorf("query = %q, want it to contain key=secret", gotQuery)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
}

// TestGeminiAPIProvider_Retry429 verifies a single retry after HTTP 429: the
// first call returns 429, the second returns 200. Uses an in-process server.
func TestGeminiAPIProvider_Retry429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error": {"message": "rate limited"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "ok"}]}}]}`))
	}))
	defer srv.Close()

	g := &GeminiAPIProvider{APIKey: "k", Model: "m", BaseURL: srv.URL}
	resp, err := g.Complete(context.Background(), Request{Prompt: "x"})
	if err != nil {
		t.Fatalf("Complete() after retry error = %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q, want %q", resp.Text, "ok")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server calls = %d, want 2 (one 429 + one retry)", got)
	}
}

// TestGeminiAPIProvider_Retry429_StillFailing verifies that a persistent 429
// (the retry also 429s) surfaces as an error rather than looping forever.
func TestGeminiAPIProvider_Retry429_StillFailing(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": {"message": "still rate limited"}}`))
	}))
	defer srv.Close()

	g := &GeminiAPIProvider{APIKey: "k", Model: "m", BaseURL: srv.URL}
	_, err := g.Complete(context.Background(), Request{Prompt: "x"})
	if err == nil {
		t.Fatal("Complete() error = nil, want error on persistent 429")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server calls = %d, want exactly 2 (initial + one retry)", got)
	}
}

func TestGeminiAPIProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = NewGeminiAPIProviderKey("k", "")
}

// TestGeminiAPIProvider_Live performs one real API call. It is opt-in: it runs
// only when SGH_LIVE=1 and GEMINI_API_KEY are both set, so normal `go test`
// never hits the network.
func TestGeminiAPIProvider_Live(t *testing.T) {
	if os.Getenv("SGH_LIVE") != "1" {
		t.Skip("set SGH_LIVE=1 to run the live gemini API call")
	}
	g, err := NewGeminiAPIProvider()
	if err != nil || !g.Available() {
		t.Skip("GEMINI_API_KEY not set; skipping live API call")
	}
	resp, err := g.Complete(context.Background(), Request{
		Prompt: `Respond with ONLY {"answer":42}.`,
	})
	if err != nil {
		t.Fatalf("live Complete() error = %v", err)
	}
	if resp.Text == "" {
		t.Errorf("live Complete() returned empty text")
	}
	t.Logf("gemini live response: %q (tokens=%d)", resp.Text, resp.TotalTokens)
}
