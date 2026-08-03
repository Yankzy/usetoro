package ase

import (
	"context"
	"fmt"

	"github.com/tidwall/gjson"
)

var (
	providerRegistry = make(map[string]ContextProvider)
)

// RegisterContextProvider registers a named ContextProvider into the global registry.
func RegisterContextProvider(p ContextProvider) {
	if p != nil {
		providerRegistry[p.Name()] = p
	}
}

// GetContextProvider retrieves a ContextProvider by name.
func GetContextProvider(name string) ContextProvider {
	return providerRegistry[name]
}

// ContextResolver handles per-node resolution and batch-level deduplicated merging.
type ContextResolver struct{}

// NewContextResolver creates a new ContextResolver.
func NewContextResolver() *ContextResolver {
	return &ContextResolver{}
}

// ResolveBatchContext runs requested ContextProviders for every node in a batch,
// then deduplicates and merges results into a single unified sharedContext map.
func (cr *ContextResolver) ResolveBatchContext(
	ctx context.Context,
	batch []*AutonomousSemanticEngineNode,
	contextConfig map[string]any,
	deps ProviderDependencies,
) (map[string]any, error) {
	mergedContext := make(map[string]any)
	if len(batch) == 0 || len(contextConfig) == 0 {
		return mergedContext, nil
	}

	for providerName, cfgRaw := range contextConfig {
		provider := GetContextProvider(providerName)
		if provider == nil {
			if deps.Logger != nil {
				deps.Logger.Warn("ContextResolver: unknown provider requested in DAG node config", "provider", providerName)
			}
			continue
		}

		providerCfg, _ := cfgRaw.(map[string]any)

		var batchResolved []any
		for _, node := range batch {
			item, err := provider.Resolve(ctx, node, providerCfg, deps)
			if err != nil {
				if deps.Logger != nil {
					deps.Logger.Warn("ContextResolver: provider resolution error", "provider", providerName, "node_id", node.NodeID, "error", err)
				}
				continue
			}
			if item != nil {
				switch v := item.(type) {
				case []any:
					batchResolved = append(batchResolved, v...)
				default:
					batchResolved = append(batchResolved, v)
				}
			}
		}

		deduped := deduplicateResolvedItems(batchResolved)
		mergedContext["candidate_"+providerName] = deduped
	}

	return mergedContext, nil
}

// deduplicateResolvedItems deduplicates a slice of items based on their unique JSON representation or key attributes.
func deduplicateResolvedItems(items []any) []any {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var result []any

	for _, item := range items {
		key := extractDeduplicationKey(item)
		if key != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		result = append(result, item)
	}

	return result
}

func extractDeduplicationKey(item any) string {
	if item == nil {
		return ""
	}
	if s, ok := item.(string); ok {
		return s
	}
	// Try converting struct / map to JSON or extracting code / display_name / name
	if m, ok := item.(map[string]any); ok {
		if code, ok := m["code"].(string); ok && code != "" {
			return "code:" + code
		}
		if name, ok := m["display_name"].(string); ok && name != "" {
			return "name:" + name
		}
		if name, ok := m["name"].(string); ok && name != "" {
			return "name:" + name
		}
	}

	// Fallback to gjson or string representation
	if bytes, err := fmt.Sprintf("%v", item), error(nil); err == nil {
		if gjson.Valid(bytes) {
			if code := gjson.Get(bytes, "code"); code.Exists() && code.String() != "" {
				return "code:" + code.String()
			}
			if name := gjson.Get(bytes, "display_name"); name.Exists() && name.String() != "" {
				return "name:" + name.String()
			}
			if name := gjson.Get(bytes, "name"); name.Exists() && name.String() != "" {
				return "name:" + name.String()
			}
		}
		return bytes
	}

	return ""
}
