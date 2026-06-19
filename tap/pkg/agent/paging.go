package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PageContext represents a single document indexed in the context pager map.
type PageContext struct {
	Type        string `json:"type"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Summary     string `json:"summary,omitempty"` // For backwards compatibility
	UUID        string `json:"-"`                 // Hidden from LLM serialization
}

// GenerateLocalContextMap generates a deterministic dictionary map bypassing UUID BPE hallucinations.
func GenerateLocalContextMap(pages []PageContext) (map[int]string, []byte) {
	localMap := make(map[int]string)

	type safePage struct {
		LocalRef    int    `json:"local_ref"`
		Type        string `json:"type"`
		Title       string `json:"title,omitempty"`
		Description string `json:"description"`
		Summary     string `json:"summary,omitempty"`
	}

	var safePages []safePage

	for index, p := range pages {
		ref := index + 1 // Start integers at 1 ensuring semantic mapping integrity
		localMap[ref] = p.UUID

		desc := p.Description
		if desc == "" && p.Summary != "" {
			desc = p.Summary
		}
		summary := p.Summary
		if summary == "" && p.Description != "" {
			summary = p.Description
		}

		safePages = append(safePages, safePage{
			LocalRef:    ref,
			Type:        p.Type,
			Title:       p.Title,
			Description: desc,
			Summary:     summary,
		})
	}

	pagesJSON, _ := json.Marshal(safePages)
	if len(pages) == 0 {
		return localMap, []byte("[]")
	}
	return localMap, pagesJSON
}

// DocumentFetcher defines the external database callback resolving UUID to raw text outside Redux bounds.
type DocumentFetcher func(ctx context.Context, uuid string) (string, error)

type pagingContextKey string

const ctxKeyTenantID pagingContextKey = "tap_tenant_id"

// WithTenantID injects the tenant ID into the context for dynamic tenant routing.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKeyTenantID, tenantID)
}

// TenantIDFromContext extracts the tenant ID from the context.
func TenantIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyTenantID).(string); ok {
		return v
	}
	return ""
}

// OKFDocumentFetcher resolves local file paths (Concept IDs) to raw Markdown body text,
// dynamically routing and isolating queries by the tenant's TenantID found in context.
func OKFDocumentFetcher(ctx context.Context, path string) (string, error) {
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		return "", fmt.Errorf("missing tenant ID in context")
	}

	// Clean and join paths safely to restrict access within the tenant's knowledge bundle
	baseDir := filepath.Clean(filepath.Join("docs", "knowledge", tenantID))
	fullPath := filepath.Clean(filepath.Join(baseDir, path))

	// Prevent directory traversal attacks
	if !strings.HasPrefix(fullPath, baseDir) {
		return "", fmt.Errorf("unauthorized path access attempt: directory traversal detected")
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read OKF concept document: %w", err)
	}

	// Strip YAML frontmatter block (enclosed in --- lines) before sending raw content to the LLM
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) == 3 {
		return strings.TrimSpace(parts[2]), nil
	}
	return string(data), nil
}

// OKFMetadata maps standard OKF YAML frontmatter tags for catalog indexing.
type OKFMetadata struct {
	Type        string   `yaml:"type"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Resource    string   `yaml:"resource"`
	Tags        []string `yaml:"tags"`
	Timestamp   string   `yaml:"timestamp"`
}

// ScanOKFCatalog walks the docs/knowledge/<tenantID> folder and builds the PageContext array
// dynamically based on the YAML frontmatter headers of each markdown document.
func ScanOKFCatalog(ctx context.Context, tenantID string) ([]PageContext, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("missing tenant ID")
	}

	baseDir := filepath.Clean(filepath.Join("docs", "knowledge", tenantID))
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return nil, nil // Return empty catalog if path doesn't exist
	}

	var pages []PageContext

	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip directories and non-markdown files
		if info.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		
		// Skip index and log files reserved under OKF SPEC
		baseName := filepath.Base(path)
		if baseName == "index.md" || baseName == "log.md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read OKF file %s: %w", path, err)
		}

		parts := strings.SplitN(string(data), "---", 3)
		if len(parts) < 3 {
			// Skip files that do not have standard OKF frontmatter delimiters
			return nil
		}

		var meta OKFMetadata
		if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
			// Skip malformed metadata files
			return nil
		}

		// OKF spec: type is REQUIRED. Skip invalid files.
		if meta.Type == "" {
			return nil
		}

		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return fmt.Errorf("failed to resolve relative path for %s: %w", path, err)
		}

		pages = append(pages, PageContext{
			Type:        meta.Type,
			Title:       meta.Title,
			Description: meta.Description,
			Summary:     meta.Description, // backwards compatibility
			UUID:        relPath,          // Concept identifier path used for PAGE_IN tool calls
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed walking OKF catalog: %w", err)
	}

	return pages, nil
}
