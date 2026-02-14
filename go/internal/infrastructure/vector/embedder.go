package vector

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// Embedder provides text embedding capabilities using OpenAI
type Embedder struct {
	client     *openai.Client
	model      string
	dimensions int
}

// NewEmbedder creates a new embedder client
// apiKey: OpenAI API key
// model: embedding model to use (e.g., "text-embedding-3-small")
// dimensions: output vector dimension (e.g., 1536)
func NewEmbedder(apiKey, model string, dimensions int) (*Embedder, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	client := openai.NewClient(apiKey)

	return &Embedder{
		client:     client,
		model:      model,
		dimensions: dimensions,
	}, nil
}

// Embed converts a single text string to a vector embedding
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Create embedding request
	req := openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.EmbeddingModel(e.model),
	}

	resp, err := e.client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned from API")
	}

	// Convert float64 to float32 for Pinecone
	embedding := resp.Data[0].Embedding
	result := make([]float32, len(embedding))
	for i, v := range embedding {
		result[i] = float32(v)
	}

	return result, nil
}

// EmbedBatch converts multiple text strings to vector embeddings
// Returns embeddings in the same order as input texts
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	// OpenAI supports batch embedding
	req := openai.EmbeddingRequest{
		Input: texts,
		Model: openai.EmbeddingModel(e.model),
	}

	resp, err := e.client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create batch embeddings: %w", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(resp.Data))
	}

	// Convert to float32 array
	results := make([][]float32, len(texts))
	for i, data := range resp.Data {
		embedding := data.Embedding
		result := make([]float32, len(embedding))
		for j, v := range embedding {
			result[j] = float32(v)
		}
		results[i] = result
	}

	return results, nil
}

// GetDimensions returns the configured embedding dimension
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

// GetModel returns the configured model name
func (e *Embedder) GetModel() string {
	return e.model
}
