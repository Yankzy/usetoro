package agents

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/spf13/viper"

	"github.com/Yankzy/usetoro/internal/database"
)

// SyncAgentConfigurations reads all .yaml files in the specified directory
// and upserts them into the toro_core.agent_configurations database table.
func SyncAgentConfigurations(ctx context.Context, queries *database.Queries, configsDir string) error {
	var walkErr error

	filepath.WalkDir(configsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		
		if d.IsDir() {
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".yaml") && !strings.HasSuffix(d.Name(), ".yml") {
			return nil
		}

		v := viper.New()
		v.SetConfigFile(path)
		
		if err := v.ReadInConfig(); err != nil {
			walkErr = fmt.Errorf("failed to read config %s: %w", path, err)
			return nil
		}

		name := v.GetString("name")
		description := v.GetString("description")
		systemPrompt := v.GetString("system_prompt")
		sdkClient := v.GetString("sdk_client")

		// Create metadata jsonb from other loosely typed variables
		// For simplicity, we just use empty JSON object or store arbitrary fields here.
		metadataBytes := []byte("{}")

		if name == "" || systemPrompt == "" {
			// Skip invalid configs
			return nil
		}

		_, dbErr := queries.UpsertAgentConfiguration(ctx, database.UpsertAgentConfigurationParams{
			Name:         name,
			Description:  pgtype.Text{String: description, Valid: description != ""},
			SystemPrompt: systemPrompt,
			SdkClient:    sdkClient,
			Metadata:     metadataBytes,
		})

		if dbErr != nil {
			walkErr = fmt.Errorf("failed to upsert config %s: %w", name, dbErr)
		}

		return nil
	})

	return walkErr
}
