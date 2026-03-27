package agent

import (
	"context"
	"encoding/json"
)

// PageContext represents a single document indexed in the context pager map.
type PageContext struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
	UUID    string `json:"-"` // Hidden from LLM serialization natively
}

// GenerateLocalContextMap generates a deterministic dictionary map bypassing UUID BPE hallucinations natively.
func GenerateLocalContextMap(pages []PageContext) (map[int]string, []byte) {
	localMap := make(map[int]string)

	type safePage struct {
		LocalRef int    `json:"local_ref"`
		Type     string `json:"type"`
		Summary  string `json:"summary"`
	}

	var safePages []safePage

	for index, p := range pages {
		ref := index + 1 // Start integers at 1 ensuring semantic mapping integrity
		localMap[ref] = p.UUID
		safePages = append(safePages, safePage{
			LocalRef: ref,
			Type:     p.Type,
			Summary:  p.Summary,
		})
	}

	pagesJSON, _ := json.Marshal(safePages)
	if len(pages) == 0 {
		return localMap, []byte("[]")
	}
	return localMap, pagesJSON
}

// DocumentFetcher defines the external database callback resolving UUID to raw text natively outside Redux bounds.
type DocumentFetcher func(ctx context.Context, uuid string) (string, error)
