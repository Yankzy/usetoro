package ase

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// COANode represents a node in the hierarchical Chart of Accounts JSON.
type COANode struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Role     string    `json:"role,omitempty"`
	Children []COANode `json:"children,omitempty"`
}

var (
	tenantCoaDAGsMu sync.RWMutex
	tenantCoaDAGs   = make(map[string]*DAG)

	// Callback invoked when a new DAG is loaded or hot-reloaded
	onCOADAGLoaded func(key string, dag *DAG)
)

// SetOnCOADAGLoaded sets a callback to be invoked when a DAG is instantiated.
func SetOnCOADAGLoaded(cb func(key string, dag *DAG)) {
	tenantCoaDAGsMu.Lock()
	defer tenantCoaDAGsMu.Unlock()
	onCOADAGLoaded = cb
}

// GetCOADAG returns the current hot-reloaded Chart of Accounts DAG for a given context.
func GetCOADAG(tenantID, realmID string) *DAG {
	tenantCoaDAGsMu.RLock()
	defer tenantCoaDAGsMu.RUnlock()

	if tenantID != "" {
		if dag, ok := tenantCoaDAGs["tenant_"+tenantID]; ok {
			return dag
		}
	}
	if realmID != "" {
		if dag, ok := tenantCoaDAGs["realm_"+realmID]; ok {
			return dag
		}
	}
	return tenantCoaDAGs["default"]
}

// setCOADAG updates the COA DAG for a specific key thread-safely.
func setCOADAG(key string, dag *DAG) {
	tenantCoaDAGsMu.Lock()
	tenantCoaDAGs[key] = dag
	cb := onCOADAGLoaded
	tenantCoaDAGsMu.Unlock()
	
	if cb != nil {
		cb(key, dag)
	}
}

// ParseCOAToDAGConfig parses the hierarchical JSON data and flattens it into a DAGConfig.
func ParseCOAToDAGConfig(jsonData []byte) (*DAGConfig, error) {
	var roots []COANode
	if err := json.Unmarshal(jsonData, &roots); err != nil {
		return nil, fmt.Errorf("failed to unmarshal COA JSON: %w", err)
	}

	nodes := make(map[string]DAGNodeConfig)
	rootChildren := make(map[string]string)

	var traverse func(node COANode)
	traverse = func(node COANode) {
		cfg := DAGNodeConfig{
			Name: node.Name,
		}

		if len(node.Children) > 0 {
			cfg.Kind = "account_selection"
			cfg.Children = make(map[string]string)
			for _, child := range node.Children {
				cfg.Children[strings.ToLower(child.Code)] = child.Code
				traverse(child)
			}
		} else {
			cfg.Kind = "terminal"
			cfg.ExecutionParams = make(map[string]string)
			if node.Role != "" {
				cfg.ExecutionParams["role"] = node.Role
			}
		}

		nodes[node.Code] = cfg
	}

	for _, root := range roots {
		rootChildren[strings.ToLower(root.Code)] = root.Code
		traverse(root)
	}

	// Create synthetic entry/root node to route to top-level classes.
	nodes["root"] = DAGNodeConfig{
		Kind:     "account_selection",
		Name:     "Root Entry Node",
		Children: rootChildren,
	}

	return &DAGConfig{
		EntryNode: "root",
		Nodes:     nodes,
	}, nil
}

// BuildDAGFromCOAJSON parses the JSON data and builds the corresponding DAG.
func BuildDAGFromCOAJSON(jsonData []byte, logger *slog.Logger) (*DAG, error) {
	cfg, err := ParseCOAToDAGConfig(jsonData)
	if err != nil {
		return nil, err
	}
	return BuildDAGFromConfig(*cfg, logger), nil
}

// loadSingleCOAFile reads and builds a DAG from a single JSON file.
func loadSingleCOAFile(file string, logger *slog.Logger) (*DAG, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	return BuildDAGFromCOAJSON(data, logger)
}

// InitCOAConfig scans for JSON files matching coa*.json or chart_of_accounts*.json and sets up a directory watcher for hot-reloading.
func InitCOAConfig(logger *slog.Logger) error {
	paths := []string{"./go/internal/erp/ase", "./internal/erp/ase", "."}
	var configDir string
	for _, p := range paths {
		if stat, err := os.Stat(p); err == nil && stat.IsDir() {
			configDir = p
			break
		}
	}

	if configDir == "" {
		return fmt.Errorf("could not find ASE config directory")
	}

	// Scan for coa*.json and chart_of_accounts*.json
	patterns := []string{"coa*.json", "chart_of_accounts*.json"}
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(configDir, pattern))
		if err == nil {
			files = append(files, matches...)
		}
	}

	if len(files) == 0 {
		if logger != nil {
			logger.Warn("No COA JSON files found in directory", "dir", configDir)
		}
	}

	for _, file := range files {
		dag, err := loadSingleCOAFile(file, logger)
		if err != nil {
			if logger != nil {
				logger.Error("failed to load initial COA JSON config", "file", file, "error", err)
			}
			continue
		}
		
		key := extractConfigKey(filepath.Base(file), "coa")
		// handle legacy naming
		if strings.HasPrefix(filepath.Base(file), "chart_of_accounts") {
			key = extractConfigKey(filepath.Base(file), "chart_of_accounts")
		}
		
		setCOADAG(key, dag)
		if logger != nil {
			logger.Info("✅ COA JSON DAG loaded", "key", key, "file", file)
		}
	}

	// Set up Hot-Reloading using fsnotify
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	if err := watcher.Add(configDir); err != nil {
		return fmt.Errorf("failed to watch config directory: %w", err)
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Watch for Write/Create events for relevant json files
				baseName := filepath.Base(event.Name)
				if (event.Op&(fsnotify.Write|fsnotify.Create) != 0) &&
					strings.HasSuffix(baseName, ".json") &&
					(strings.HasPrefix(baseName, "coa") || strings.HasPrefix(baseName, "chart_of_accounts")) {
					
					if logger != nil {
						logger.Info("🔄 COA JSON change detected, reloading COA DAG", "file", event.Name)
					}
					
					// Slight delay to allow file write to finish
					time.Sleep(100 * time.Millisecond)
					
					dag, err := loadSingleCOAFile(event.Name, logger)
					if err != nil {
						if logger != nil {
							logger.Error("Failed to reload COA JSON", "error", err, "file", event.Name)
						}
						continue
					}
					
					key := extractConfigKey(baseName, "coa")
					if strings.HasPrefix(baseName, "chart_of_accounts") {
						key = extractConfigKey(baseName, "chart_of_accounts")
					}
					
					setCOADAG(key, dag)
					if logger != nil {
						logger.Info("✅ COA JSON DAG hot-reloaded successfully", "key", key)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				if logger != nil {
					logger.Error("fsnotify watcher error", "error", err)
				}
			}
		}
	}()

	return nil
}
