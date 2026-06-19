package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type COANode struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Children []COANode `json:"children,omitempty"`
}

func normalizeCode(code, name string) string {
	if code == "11" && strings.Contains(name, "Reopening") {
		return "011"
	}
	if code == "12" && strings.Contains(name, "Reopening") {
		return "012"
	}
	if code == "124/25" && strings.Contains(name, "Reopening") {
		return "0124/25"
	}
	if code == "21" && strings.Contains(name, "Closing") {
		return "021"
	}
	if code == "22" && strings.Contains(name, "Closing") {
		return "022"
	}
	if code == "224/25" && strings.Contains(name, "Closing") {
		return "0224/25"
	}
	return code
}

func isImmediateChild(parentCode, childCode string, allCodes []string) bool {
	if !strings.HasPrefix(childCode, parentCode) || len(childCode) <= len(parentCode) {
		return false
	}
	// Check if there is any intermediate prefix in the list
	for _, c := range allCodes {
		if c == parentCode || c == childCode {
			continue
		}
		if strings.HasPrefix(c, parentCode) && len(c) > len(parentCode) &&
			strings.HasPrefix(childCode, c) && len(childCode) > len(c) {
			return false
		}
	}
	return true
}

func cleanSnakeCase(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}
	words := strings.Fields(sb.String())
	var cleanWords []string
	for _, w := range words {
		if w == "on" || w == "of" || w == "and" || w == "the" || w == "for" || w == "with" || w == "or" {
			continue
		}
		cleanWords = append(cleanWords, w)
	}
	return strings.Join(cleanWords, "_")
}

func cleanClassNum(classCode string) string {
	return strings.TrimSpace(strings.ReplaceAll(classCode, "Class", ""))
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@db:5432/toro?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	defer pool.Close()

	// Read Moroccan COA JSON
	jsonFile, err := os.Open("go/internal/erp/ase/chart_of_accounts_morocco.json")
	if err != nil {
		log.Fatalf("failed to open json: %v", err)
	}
	defer jsonFile.Close()

	jsonData, err := io.ReadAll(jsonFile)
	if err != nil {
		log.Fatalf("failed to read json: %v", err)
	}

	var roots []COANode
	if err := json.Unmarshal(jsonData, &roots); err != nil {
		log.Fatalf("failed to parse json: %v", err)
	}

	dagNodes := make(map[string]ase.DAGNodeConfig)
	rootChildren := make(map[string]string)

	for _, classNode := range roots {
		classNum := cleanClassNum(classNode.Code)
		if classNum == "0" || classNum == "8" || classNum == "9" {
			continue // Skip Class 0 (Special Accounts), Class 8 (Results), and Class 9 (Analytical Accounting)
		}
		classID := fmt.Sprintf("class_%s_%s", classNum, cleanSnakeCase(classNode.Name))
		rootChildren[classNode.Name] = classID

		classCfg := ase.DAGNodeConfig{
			Kind:              "account_type",
			Name:              classNode.Name,
			BatchSize:         50,
			BatchFlushSeconds: 5,
			PromptKey:         classID + "_specialist",
			EdgeType:          "static",
			Children:          make(map[string]string),
		}

		for _, subNode := range classNode.Children {
			subID := fmt.Sprintf("%s_%s", cleanSnakeCase(subNode.Name), subNode.Code)
			classCfg.Children[strings.ToUpper(subNode.Name)] = subID

			subCfg := ase.DAGNodeConfig{
				Kind:              "account_type",
				Name:              subNode.Name,
				BatchSize:         50,
				BatchFlushSeconds: 5,
				PromptKey:         cleanSnakeCase(subNode.Name) + "_specialist",
				EdgeType:          "static",
				Children:          make(map[string]string),
			}

			// Gather accounts under this subcategory
			type RawAccount struct {
				Code           string
				NormalizedCode string
				Name           string
			}
			var accounts []RawAccount
			var allNormalized []string
			for _, acc := range subNode.Children {
				normalized := normalizeCode(acc.Code, acc.Name)
				accounts = append(accounts, RawAccount{
					Code:           acc.Code,
					NormalizedCode: normalized,
					Name:           acc.Name,
				})
				allNormalized = append(allNormalized, normalized)
			}

			// Run prefix matching to find parent-child links within the subcategory
			for _, acc := range accounts {
				accID := fmt.Sprintf("account_%s", strings.ReplaceAll(acc.Code, "/", "_"))

				// Find immediate prefix parent
				var parent *RawAccount
				for i, other := range accounts {
					if other.NormalizedCode == acc.NormalizedCode {
						continue
					}
					if isImmediateChild(other.NormalizedCode, acc.NormalizedCode, allNormalized) {
						parent = &accounts[i]
						break
					}
				}

				if parent != nil {
					parentID := fmt.Sprintf("account_%s", strings.ReplaceAll(parent.Code, "/", "_"))

					// Ensure parent configuration exists and is marked as passthrough
					pCfg, exists := dagNodes[parentID]
					if !exists {
						pCfg = ase.DAGNodeConfig{
							Kind:              "passthrough",
							Name:              parent.Name,
							BatchSize:         50,
							BatchFlushSeconds: 5,
							PromptKey:         parentID + "_specialist",
							EdgeType:          "static",
							Children:          make(map[string]string),
						}
					} else {
						pCfg.Kind = "passthrough"
					}

					// Deduplicate child names in parent's children map
					key := acc.Name
					if key == parent.Name {
						key = key + " (specific)"
					}
					origKey := key
					counter := 1
					for {
						if _, taken := pCfg.Children[key]; !taken {
							break
						}
						counter++
						key = fmt.Sprintf("%s (specific %d)", origKey, counter)
					}
					pCfg.Children[key] = accID
					dagNodes[parentID] = pCfg

				} else {
					// Top-level account directly under the subcategory node
					key := acc.Name
					if key == subNode.Name {
						key = key + " (specific)"
					}
					origKey := key
					counter := 1
					for {
						if _, taken := subCfg.Children[key]; !taken {
							break
						}
						counter++
						key = fmt.Sprintf("%s (specific %d)", origKey, counter)
					}
					subCfg.Children[key] = accID
				}

				// Ensure child node exists
				if _, exists := dagNodes[accID]; !exists {
					dagNodes[accID] = ase.DAGNodeConfig{
						Kind:              "terminal",
						Name:              acc.Name,
						BatchSize:         50,
						BatchFlushSeconds: 5,
						PromptKey:         accID + "_specialist",
						EdgeType:          "static",
						Children:          make(map[string]string),
					}
				}
			}

			// Save subcategory node
			dagNodes[subID] = subCfg
		}

		// Save class node
		dagNodes[classID] = classCfg
	}

	// Create root entry node
	dagNodes["root"] = ase.DAGNodeConfig{
		Kind:              "account_selection",
		Name:              "Root Entry Node",
		BatchSize:         50,
		BatchFlushSeconds: 5,
		PromptKey:         "root_specialist",
		EdgeType:          "static",
		Children:          rootChildren,
	}

	dagConfig := ase.DAGConfig{
		EntryNode: "root",
		Nodes:     dagNodes,
	}

	dagConfigBytes, err := json.Marshal(dagConfig)
	if err != nil {
		log.Fatalf("failed to marshal dag config: %v", err)
	}

	hyperParams := map[string]interface{}{
		"confidence_threshold":     0.98,
		"auto_advance":             true,
		"max_llm_retries":          3,
		"llm_timeout_seconds":      120,
		"batch_flush_seconds":      5,
		"active_agent_ttl_minutes": 10,
		"lock_ttl_seconds":         30,
	}
	hyperParamsBytes, _ := json.Marshal(hyperParams)

	prompts := map[string]string{
		"root_specialist": "Identify the primary account class for this transaction.",
	}
	promptsBytes, _ := json.Marshal(prompts)

	db := database.New(pool)
	_, err = db.UpsertASEConfig(ctx, database.UpsertASEConfigParams{
		TenantID:        pgtype.UUID{Valid: false},
		RealmID:         pgtype.Text{Valid: false},
		Name:            "morocco",
		DagConfig:       dagConfigBytes,
		HyperParameters: hyperParamsBytes,
		Prompts:         promptsBytes,
	})
	if err != nil {
		log.Fatalf("failed to upsert morocco config: %v", err)
	}

	fmt.Println("✅ Successfully compiled Moroccan COA JSON and seeded 'morocco' DAG into database!")
}
