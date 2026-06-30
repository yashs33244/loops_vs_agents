// Package provider abstracts the LLM backend that executes a node's work. A
// Provider turns a Request (prompt + system prompt) into a Response (text +
// token accounting), independent of whether the model is reached via a CLI or
// an HTTP API.
//
// Two real scenarios from the spec are supported behind one interface:
//
//   - CLI scenario: ClaudeCLIProvider shells out to `claude -p` (decision D4:
//     exec.CommandContext with an arg slice, prompt via stdin, no shell).
//   - API scenario: GeminiAPIProvider calls the HTTP generateContent endpoint
//     with a key from GEMINI_API_KEY.
//
// MockProvider gives deterministic, scriptable responses for tests.
package provider
