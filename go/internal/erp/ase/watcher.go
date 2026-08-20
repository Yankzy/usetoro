package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// LoadFromDir performs an initial full sync of all .yml files in the given directory.
func LoadFromDir(ctx context.Context, dirPath string) error {
	if dbQueries == nil {
		return fmt.Errorf("LoadFromDir called but dbQueries is nil")
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			if logger != nil {
				logger.Warn("ASE: dag config directory does not exist", "path", dirPath)
			}
			return nil
		}
		return fmt.Errorf("read dag config directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		filePath := filepath.Join(dirPath, entry.Name())
		if err := UpsertDAGFromFile(ctx, filePath); err != nil {
			if logger != nil {
				logger.Error("ASE: failed to load dag file during init", "file", filePath, "error", err)
			}
		}
	}

	if logger != nil {
		logger.Info("✅ ASE: synced DAG configs from directory", "path", dirPath)
	}
	return nil
}

// WatchDAGs watches the given directory for changes to .yml files and hot-reloads them into the database.
func WatchDAGs(ctx context.Context, dirPath string) error {
	if dbQueries == nil {
		return fmt.Errorf("WatchDAGs called but dbQueries is nil")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	if err := watcher.Add(dirPath); err != nil {
		if os.IsNotExist(err) {
			if logger != nil {
				logger.Warn("ASE: Cannot watch dag configs because directory does not exist", "path", dirPath)
			}
			return nil
		}
		return fmt.Errorf("add dir to watcher: %w", err)
	}

	if logger != nil {
		logger.Info("👁️  ASE: watching dag config directory for changes", "path", dirPath)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if logger != nil {
				logger.Info("RAW DAG WATCHER EVENT", "name", event.Name, "op", event.Op.String())
			}
			if filepath.Ext(event.Name) != ".yml" {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Chmod) != 0 {
				if logger != nil {
					logger.Info("🔄 ASE: DAG file change detected", "file", event.Name)
				}
				// Debounce
				time.Sleep(200 * time.Millisecond)

				if err := UpsertDAGFromFile(ctx, event.Name); err != nil {
					if logger != nil {
						logger.Error("❌ ASE: failed to hot-reload dag config", "file", event.Name, "error", err)
					}
				} else {
					if logger != nil {
						logger.Info("🚀 ASE: hot-reload complete", "file", event.Name)
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			if logger != nil {
				logger.Error("ASE: watcher error", "error", err)
			}
		}
	}
}

// UpsertDAGFromFile parses a DAG YAML file and saves it to the database as the global configuration for its basename.
func UpsertDAGFromFile(ctx context.Context, filePath string) error {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("yaml unmarshal: %w", err)
	}

	// Extract sections, default to empty maps if missing
	var dagMap, hpMap, promptsMap interface{}

	if val, ok := raw["dag"]; ok {
		dagMap = val
	} else {
		dagMap = map[string]interface{}{}
	}

	if val, ok := raw["hyper_parameters"]; ok {
		hpMap = val
	} else {
		hpMap = map[string]interface{}{}
	}

	if val, ok := raw["prompts"]; ok {
		promptsMap = val
	} else {
		promptsMap = map[string]interface{}{}
	}

	// Marshal into JSON for database
	dagBytes, err := json.Marshal(dagMap)
	if err != nil {
		return fmt.Errorf("marshal dag to json: %w", err)
	}

	hpBytes, err := json.Marshal(hpMap)
	if err != nil {
		return fmt.Errorf("marshal hyper_parameters to json: %w", err)
	}

	promptsBytes, err := json.Marshal(promptsMap)
	if err != nil {
		return fmt.Errorf("marshal prompts to json: %w", err)
	}
	baseName := filepath.Base(filePath)
	dagName := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	// dagName := "ase_gaap_us"

	// The watcher syncs the 'global' configuration fallback (user_id IS NULL).
	_, err = dbQueries.UpsertASEConfig(ctx, database.UpsertASEConfigParams{
		UserID:          pgtype.UUID{Valid: false},
		Name:            dagName,
		DagConfig:       dagBytes,
		HyperParameters: hpBytes,
		Prompts:         promptsBytes,
	})
	if err != nil {
		return fmt.Errorf("upsert db: %w", err)
	}

	InvalidateConfigCache("", dagName)

	// Clear cache for this global DAG
	if configCache != nil {
		configCache.Remove(dagName)
	}

	return nil
}
