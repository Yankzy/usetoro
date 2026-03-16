package vector

import (
	"context"
	"fmt"

	"strings"
	"time"

	"github.com/pinecone-io/go-pinecone/v4/pinecone"
	"google.golang.org/protobuf/types/known/structpb"
)

// PineconeClient provides full lifecycle management for Pinecone vector operations
type PineconeClient struct {
	client    *pinecone.Client
	indexName string
	dimension int
	host      string
}

// Vector represents a vector to be upserted to Pinecone
type Vector struct {
	ID       string
	Values   []float32
	Metadata map[string]interface{}
}

// Match represents a query result from Pinecone
type Match struct {
	ID       string
	Score    float64
	Metadata map[string]interface{}
}

// IndexStats represents index statistics
type IndexStats struct {
	TotalVectorCount int64
	Namespaces       map[string]int64
}

// NewPineconeClient initializes a new Pinecone client
// apiKey: Pinecone API key
// indexName: name of the index to use
// dimension: vector dimension (e.g., 3072 for OpenAI text-embedding-3-large)
func NewPineconeClient(apiKey, indexName string, dimension int) (*PineconeClient, error) {
	ctx := context.Background()

	// Initialize Pinecone client
	pc, err := pinecone.NewClient(pinecone.NewClientParams{
		ApiKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create pinecone client: %w", err)
	}

	client := &PineconeClient{
		client:    pc,
		indexName: indexName,
		dimension: dimension,
	}

	// Ensure index exists with a timeout
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.EnsureIndex(ctxTimeout); err != nil {
		return nil, fmt.Errorf("failed to ensure index exists: %w", err)
	}

	// Host string is required for index operations.
	// Cache it here to avoid calling control plane DescribeIndex on every data plane operation.
	idx, err := client.client.DescribeIndex(ctx, indexName)
	if err != nil {
		return nil, fmt.Errorf("failed to describe pinecone index to get host: %w", err)
	}
	client.host = idx.Host

	return client, nil
}

// EnsureIndex creates the index if it doesn't exist
func (p *PineconeClient) EnsureIndex(ctx context.Context) error {
	// List existing indexes
	indexes, err := p.client.ListIndexes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}

	// Check if index already exists
	for _, idx := range indexes {
		if idx.Name == p.indexName {
			// Index exists, verify dimension matches
			if idx.Dimension != nil && *idx.Dimension != int32(p.dimension) {
				return fmt.Errorf("index %s exists with dimension %d, expected %d",
					p.indexName, *idx.Dimension, p.dimension)
			}
			return nil
		}
	}

	// Create serverless index
	dimension := int32(p.dimension)
	metric := pinecone.Cosine
	_, err = p.client.CreateServerlessIndex(ctx, &pinecone.CreateServerlessIndexRequest{
		Name:      p.indexName,
		Dimension: &dimension,
		Metric:    &metric,
		Cloud:     pinecone.Aws,
		Region:    "us-east-1", // Use region from config if needed
	})

	if err != nil {
		// Log the error but don't fail, as free tier returns 403 if max indexes reached,
		// and the index might already exist in another region or we might just be
		// trying to recreate what we can't see locally.
		if strings.Contains(err.Error(), "FORBIDDEN") || strings.Contains(err.Error(), "max serverless indexes") {
			return nil
		}
		// Also ignore ALREADY_EXISTS 409 errors
		if strings.Contains(err.Error(), "ALREADY_EXISTS") {
			return nil
		}
		return fmt.Errorf("failed to create index: %w", err)
	}

	return nil
}

// UpsertVectors inserts or updates vectors in a specific namespace
func (p *PineconeClient) UpsertVectors(ctx context.Context, namespace string, vectors []Vector) error {
	if len(vectors) == 0 {
		return nil
	}

	// Get index connection
	idxConn, err := p.client.Index(pinecone.NewIndexConnParams{
		Host:      p.getIndexHost(),
		Namespace: namespace,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to index: %w", err)
	}

	// Convert to Pinecone vector format
	pineconeVectors := make([]*pinecone.Vector, len(vectors))
	for i, v := range vectors {
		metadata, err := structpb.NewStruct(v.Metadata)
		if err != nil {
			return fmt.Errorf("failed to convert metadata for vector %s: %w", v.ID, err)
		}

		pineconeVectors[i] = &pinecone.Vector{
			Id:       v.ID,
			Values:   &v.Values,
			Metadata: metadata,
		}
	}

	// Upsert vectors
	_, err = idxConn.UpsertVectors(ctx, pineconeVectors)
	if err != nil {
		return fmt.Errorf("failed to upsert vectors: %w", err)
	}

	return nil
}

// QueryVectors performs a semantic search
// namespace: the namespace to query (typically realmID)
// vector: the query vector
// topK: number of results to return
// filter: metadata filters (e.g., {"account_type": "Expense"})
func (p *PineconeClient) QueryVectors(ctx context.Context, namespace string, vector []float32, topK int, filter map[string]interface{}) ([]Match, error) {
	// Get index connection
	idxConn, err := p.client.Index(pinecone.NewIndexConnParams{
		Host:      p.getIndexHost(),
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to index: %w", err)
	}

	// Note: Filter will be applied post-query in application layer

	// Query
	// Note: Pinecone v4 doesn't support Filter in QueryByVectorValuesRequest
	// Filtering must be done post-query in application layer for now
	response, err := idxConn.QueryByVectorValues(ctx, &pinecone.QueryByVectorValuesRequest{
		Vector:          vector,
		TopK:            uint32(topK),
		IncludeMetadata: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query vectors: %w", err)
	}

	// Convert response to matches and apply filter if provided
	var matches []Match
	for _, match := range response.Matches {
		metadata := make(map[string]interface{})
		if match.Vector.Metadata != nil {
			metadata = match.Vector.Metadata.AsMap()
		}

		// Apply filter post-query if provided
		if len(filter) > 0 {
			matchesFilter := true
			for key, value := range filter {
				if metaVal, ok := metadata[key]; !ok || metaVal != value {
					matchesFilter = false
					break
				}
			}
			if !matchesFilter {
				continue
			}
		}

		matches = append(matches, Match{
			ID:       match.Vector.Id,
			Score:    float64(match.Score),
			Metadata: metadata,
		})
	}

	return matches, nil
}

// DeleteByNamespace deletes all vectors in a namespace (cleanup when client disconnects)
func (p *PineconeClient) DeleteByNamespace(ctx context.Context, namespace string) error {
	idxConn, err := p.client.Index(pinecone.NewIndexConnParams{
		Host:      p.getIndexHost(),
		Namespace: namespace,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to index: %w", err)
	}

	// Delete all vectors in namespace
	err = idxConn.DeleteAllVectorsInNamespace(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	return nil
}

// DeleteByIDs deletes specific vectors by their IDs
func (p *PineconeClient) DeleteByIDs(ctx context.Context, namespace string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	idxConn, err := p.client.Index(pinecone.NewIndexConnParams{
		Host:      p.getIndexHost(),
		Namespace: namespace,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to index: %w", err)
	}

	err = idxConn.DeleteVectorsById(ctx, ids)
	if err != nil {
		return fmt.Errorf("failed to delete vectors: %w", err)
	}

	return nil
}

// DescribeIndexStats returns statistics about the index
func (p *PineconeClient) DescribeIndexStats(ctx context.Context) (*IndexStats, error) {
	idxConn, err := p.client.Index(pinecone.NewIndexConnParams{
		Host: p.getIndexHost(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to index: %w", err)
	}

	stats, err := idxConn.DescribeIndexStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get index stats: %w", err)
	}

	// Convert namespace stats
	namespaces := make(map[string]int64)
	if stats.Namespaces != nil {
		for ns, nsStats := range stats.Namespaces {
			namespaces[ns] = int64(nsStats.VectorCount)
		}
	}

	return &IndexStats{
		TotalVectorCount: int64(stats.TotalVectorCount),
		Namespaces:       namespaces,
	}, nil
}

// getIndexHost retrieves the host URL for the index
func (p *PineconeClient) getIndexHost() string {
	return p.host
}

// Close closes the Pinecone client connection
func (p *PineconeClient) Close() error {
	// Pinecone v4 client doesn't require explicit closure
	// but we keep this method for future compatibility
	return nil
}
