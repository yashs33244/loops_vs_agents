package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// defaultGeminiModel is used when neither the Request nor the provider config
// names a model.
const defaultGeminiModel = "gemini-3.1-flash-lite"

// geminiBaseURL is the Generative Language API host. Overridable in tests via
// the BaseURL field so no real network call is ever made under `go test`.
const geminiBaseURL = "https://generativelanguage.googleapis.com"

// rate429Backoff is the short pause before the single retry on HTTP 429.
const rate429Backoff = 500 * time.Millisecond

// GeminiAPIProvider executes completions via the HTTP generateContent endpoint.
// The key is read from GEMINI_API_KEY (or supplied to the constructor) and is
// never logged. It retries once on HTTP 429 after a short backoff.
type GeminiAPIProvider struct {
	// APIKey is the Gemini API key. When empty the constructor reads
	// GEMINI_API_KEY from the environment.
	APIKey string

	// Model is the default model id; empty means defaultGeminiModel. A non-empty
	// Request.Model overrides it per call.
	Model string

	// BaseURL overrides the API host (tests point this at a local httptest
	// server). Empty means geminiBaseURL.
	BaseURL string

	// Client is the HTTP client used for requests; nil means http.DefaultClient.
	Client *http.Client
}

// NewGeminiAPIProvider builds a provider, reading the key from GEMINI_API_KEY.
// It returns an error if no key is set.
func NewGeminiAPIProvider() (*GeminiAPIProvider, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, errors.New("GEMINI_API_KEY not set")
	}
	return &GeminiAPIProvider{APIKey: key, Model: defaultGeminiModel}, nil
}

// NewGeminiAPIProviderKey builds a provider with an explicit key and model
// (empty model means defaultGeminiModel).
func NewGeminiAPIProviderKey(key, model string) *GeminiAPIProvider {
	if model == "" {
		model = defaultGeminiModel
	}
	return &GeminiAPIProvider{APIKey: key, Model: model}
}

// Name reports the provider name.
func (g *GeminiAPIProvider) Name() string { return "gemini" }

// Available reports whether an API key is configured. Tests gate live calls
// behind this.
func (g *GeminiAPIProvider) Available() bool { return g.APIKey != "" }

func (g *GeminiAPIProvider) model(req Request) string {
	if req.Model != "" {
		return req.Model
	}
	if g.Model != "" {
		return g.Model
	}
	return defaultGeminiModel
}

func (g *GeminiAPIProvider) baseURL() string {
	if g.BaseURL != "" {
		return g.BaseURL
	}
	return geminiBaseURL
}

func (g *GeminiAPIProvider) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

// geminiResponse mirrors the slice of the generateContent JSON we consume. It
// is exported-by-shape only; the parsing helper below is what tests exercise.
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseGeminiResponse turns a generateContent response body into a Response.
// statusCode is the HTTP status that accompanied the body. It is the single
// pure unit tested offline. A nil error means a usable Response.
func parseGeminiResponse(body []byte, statusCode int) (Response, error) {
	var gr geminiResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return Response{}, fmt.Errorf("decode gemini response: %w", err)
	}
	if gr.Error != nil {
		return Response{}, fmt.Errorf("gemini api error: %s", gr.Error.Message)
	}
	if statusCode != http.StatusOK {
		return Response{}, fmt.Errorf("gemini http %d", statusCode)
	}

	text := ""
	if len(gr.Candidates) > 0 && len(gr.Candidates[0].Content.Parts) > 0 {
		text = gr.Candidates[0].Content.Parts[0].Text
	}
	return Response{
		Text:         text,
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		TotalTokens:  gr.UsageMetadata.TotalTokenCount,
	}, nil
}

// buildRequestBody assembles the generateContent JSON payload. responseMimeType
// is set to application/json so structured output is not a matter of prompt
// luck (proven in the spike). A non-empty system prompt becomes a
// systemInstruction; MaxTokens (when >0) caps output tokens.
func buildRequestBody(req Request) []byte {
	gen := map[string]any{"responseMimeType": "application/json"}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	payload := map[string]any{
		"contents": []any{
			map[string]any{"parts": []any{map[string]any{"text": req.Prompt}}},
		},
		"generationConfig": gen,
	}
	if req.SystemPrompt != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": req.SystemPrompt}},
		}
	}
	b, _ := json.Marshal(payload)
	return b
}

// Complete calls the HTTP API for one completion, retrying once on HTTP 429.
func (g *GeminiAPIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		g.baseURL(), g.model(req), g.APIKey)
	body := buildRequestBody(req)

	resp, status, err := g.do(ctx, url, body)
	if err != nil {
		return Response{}, err
	}
	if status == http.StatusTooManyRequests {
		// Retry once after a short backoff (interruptible by ctx).
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(rate429Backoff):
		}
		resp, status, err = g.do(ctx, url, body)
		if err != nil {
			return Response{}, err
		}
	}
	return parseGeminiResponse(resp, status)
}

// do performs a single POST and returns the body and status code.
func (g *GeminiAPIProvider) do(ctx context.Context, url string, body []byte) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client().Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return b, resp.StatusCode, nil
}
