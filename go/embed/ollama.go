// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// OllamaEmbedder embeds text via an Ollama HTTP server. The default endpoint
// is http://127.0.0.1:11434 — override with the OLLAMA_HOST env var (Ollama's
// own convention) or by passing Endpoint to NewOllama.
type OllamaEmbedder struct {
	Endpoint string
	Model    string
	Client   *http.Client

	dimOnce sync.Once
	dim     int
	dimErr  error
}

// NewOllama returns an OllamaEmbedder. model is the Ollama model name, e.g.
// "nomic-embed-text". endpoint may be empty to use $OLLAMA_HOST or the
// 127.0.0.1:11434 default.
func NewOllama(endpoint, model string) *OllamaEmbedder {
	if endpoint == "" {
		endpoint = os.Getenv("OLLAMA_HOST")
		if endpoint == "" {
			endpoint = "http://127.0.0.1:11434"
		}
	}
	return &OllamaEmbedder{
		Endpoint: endpoint,
		Model:    model,
		Client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// ModelID returns "ollama:<model>" so different local model choices don't
// share a vector cache.
func (o *OllamaEmbedder) ModelID() string {
	return "ollama:" + o.Model
}

// Dim probes the embedder with a one-token input on first call and caches
// the result.
func (o *OllamaEmbedder) Dim(ctx context.Context) (int, error) {
	o.dimOnce.Do(func() {
		vs, err := o.Embed(ctx, []string{"probe"})
		if err != nil {
			o.dimErr = err
			return
		}
		if len(vs) == 0 || len(vs[0]) == 0 {
			o.dimErr = fmt.Errorf("embed: empty probe result")
			return
		}
		o.dim = len(vs[0])
	})
	return o.dim, o.dimErr
}

type ollamaEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResp struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed sends a single batch to the /api/embed endpoint. Ollama accepts a
// string or string-array; we always pass an array so the response shape is
// consistent.
func (o *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(ollamaEmbedReq{Model: o.Model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshalling embed request: %w", err)
	}
	url := o.Endpoint + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling ollama %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama %s returned %d: %s", url, resp.StatusCode, string(buf))
	}
	var parsed ollamaEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decoding ollama response: %w", err)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(parsed.Embeddings), len(texts))
	}
	return parsed.Embeddings, nil
}
