package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/infra/vector"
)

func loadEnvFile(filepath string) {
	b, err := os.ReadFile(filepath)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			v = strings.Trim(v, `"'`)
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

func main() {
	loadEnvFile(".env")
	loadEnvFile("../.env")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@localhost:5432/toro?sslmode=disable"
	}

	urlsToTry := []string{
		dbURL,
		strings.Replace(dbURL, "@db:5432", "@localhost:5435", 1),
		strings.Replace(dbURL, "@db:5432", "@localhost:5432", 1),
		strings.Replace(dbURL, "@torodb:5432", "@localhost:5435", 1),
		strings.Replace(dbURL, "@torodb:5432", "@localhost:5432", 1),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var pool *pgxpool.Pool
	var connectedURL string
	var err error

	for _, url := range urlsToTry {
		log.Printf("Connecting to DB at: %s ...", url)
		pool, err = pgxpool.New(ctx, url)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				connectedURL = url
				break
			}
			pool.Close()
		}
	}

	if connectedURL == "" {
		log.Fatalf("Failed to connect to database on any tested URL. Last error: %v", err)
	}

	log.Printf("Successfully connected to database at: %s", connectedURL)
	defer pool.Close()

	// Set search path so postgres resolves vector and operators automatically
	if _, err := pool.Exec(ctx, `SET search_path TO toro_core, public;`); err != nil {
		log.Printf("Warning: failed to set search_path: %v", err)
	}

	// Step 1: Check Installed Extensions & Schemas
	log.Println("\n--- STEP 1: Checking Extensions & Schemas ---")
	rows, err := pool.Query(ctx, `
		SELECT e.extname, e.extversion, n.nspname 
		FROM pg_extension e 
		JOIN pg_namespace n ON n.oid = e.extnamespace 
		WHERE e.extname IN ('vector', 'alloydb_scann', 'scann')`)
	if err != nil {
		log.Fatalf("Failed to query pg_extension: %v", err)
	}

	for rows.Next() {
		var name, ver, schema string
		if err := rows.Scan(&name, &ver, &schema); err == nil {
			log.Printf("  [FOUND EXTENSION] %s (v%s) in schema %q", name, ver, schema)
		}
	}
	rows.Close()

	// Step 2: Ensure Table Exists
	log.Println("\n--- STEP 2: Schema & Table Verification ---")
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS toro_core;`); err != nil {
		log.Fatalf("Failed to create schema toro_core: %v", err)
	}

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS toro_core.ase_vector_memory (
		id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		realm_id      TEXT NOT NULL,
		source_type   TEXT NOT NULL CHECK (source_type IN ('memory_rule', 'resolved_tx')),
		raw_text      TEXT NOT NULL,
		embedding     toro_core.vector(1536),
		source_row_id UUID NOT NULL,
		metadata      JSONB NOT NULL DEFAULT '{}',
		embedded_at   TIMESTAMPTZ,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT uq_ase_vector_memory UNIQUE (realm_id, source_type, source_row_id)
	);`

	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		log.Fatalf("Failed to create toro_core.ase_vector_memory table: %v", err)
	}
	log.Println("Table toro_core.ase_vector_memory verified.")

	// Step 3: Embed & Store Sample Records
	log.Println("\n--- STEP 3: Embed & Upsert Sample Situations ---")
	apiKey := os.Getenv("OPENAI_API_KEY")
	var embedder *vector.Embedder
	if apiKey != "" {
		embedder, err = vector.NewEmbedder(apiKey, "text-embedding-3-small", 1536)
		if err != nil {
			log.Printf("Failed to initialize OpenAI Embedder: %v", err)
		} else {
			log.Println("Initialized OpenAI Embedder (text-embedding-3-small, 1536 dims).")
		}
	} else {
		log.Println("[WARNING] OPENAI_API_KEY not set.")
	}

	testRealmID := "test-realm-scann-verification"

	testCases := []struct {
		Text     string
		Category string
	}{
		// Office expenses
		{Text: "Office Supplies - Purchase of printer paper and ink cartridges from Staples", Category: "OFFICE_EXPENSE"},
		{Text: "Paper towels, disinfectant wipes, and trash bags for office breakroom", Category: "OFFICE_EXPENSE"},
		{Text: "Desk chair replacement and ergonomic mouse pad purchase", Category: "OFFICE_EXPENSE"},

		// Cloud / Hosting
		{Text: "AWS Cloud Infrastructure hosting bill for production EC2 instances", Category: "HOSTING_EXPENSE"},
		{Text: "Google Cloud Platform GCP BigQuery and Kubernetes Engine monthly charges", Category: "HOSTING_EXPENSE"},
		{Text: "DigitalOcean droplet cloud server hosting invoice", Category: "HOSTING_EXPENSE"},

		// Revenue / Invoices
		{Text: "Client Payment Received - Invoice #1042 for software development services", Category: "REVENUE"},
		{Text: "Wire transfer received from Acme Corp for Q2 consulting retainer", Category: "REVENUE"},
		{Text: "Stripe payout received for customer SaaS monthly subscriptions", Category: "REVENUE"},

		// Meals
		{Text: "Monthly Team Lunch catering expense at Downtown Bistro", Category: "MEALS_EXPENSE"},
		{Text: "Coffee and pastries for client strategy workshop session", Category: "MEALS_EXPENSE"},
		{Text: "Dinner meeting expense with prospective enterprise partner", Category: "MEALS_EXPENSE"},

		// Software
		{Text: "Subscription payment for GitHub Enterprise Organization seats", Category: "SOFTWARE_EXPENSE"},
		{Text: "Slack Pro workspace subscription monthly renewal charge", Category: "SOFTWARE_EXPENSE"},
		{Text: "Notion Enterprise team workspace annual software renewal", Category: "SOFTWARE_EXPENSE"},
	}

	insertedRows := 0
	for i, tc := range testCases {
		sourceID := uuid.NewMD5(uuid.NameSpaceDNS, []byte(fmt.Sprintf("test-row-%d", i)))

		var vec []float32
		if embedder != nil {
			vec, err = embedder.Embed(ctx, tc.Text)
			if err != nil {
				log.Printf("Failed to embed text %q: %v", tc.Text, err)
			}
		}

		if len(vec) == 0 {
			vec = make([]float32, 1536)
			vec[i%1536] = 1.0
		}

		vecLiteral := floatsToLiteral(vec)
		metaJSON := fmt.Sprintf(`{"category": "%s"}`, tc.Category)
		upsertSQL := `
		INSERT INTO toro_core.ase_vector_memory
			(realm_id, source_type, raw_text, embedding, source_row_id, metadata, embedded_at)
		VALUES ($1, $2, $3, $4::toro_core.vector, $5, $6, NOW())
		ON CONFLICT (realm_id, source_type, source_row_id)
		DO UPDATE SET raw_text = EXCLUDED.raw_text, embedding = EXCLUDED.embedding, metadata = EXCLUDED.metadata;`

		_, err = pool.Exec(ctx, upsertSQL, testRealmID, "resolved_tx", tc.Text, vecLiteral, sourceID, metaJSON)
		if err != nil {
			log.Fatalf("Failed to insert row %d: %v", i, err)
		}
		insertedRows++
		log.Printf("  [INSERTED %2d/%2d] %-65s -> %s", insertedRows, len(testCases), tc.Text, tc.Category)
	}

	log.Printf("Successfully inserted %d vector memory rows.", insertedRows)

	// Step 4: ScaNN Index Creation
	log.Println("\n--- STEP 4: Creating AlloyDB ScaNN Index ---")
	scannSQL := `
	CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_scann
	ON toro_core.ase_vector_memory USING scann (embedding cosine)
	WITH (num_leaves = 2)
	WHERE embedding IS NOT NULL;`

	log.Println("Executing: CREATE INDEX ... USING scann (embedding cosine) WITH (num_leaves = 2) ...")
	if _, err := pool.Exec(ctx, scannSQL); err != nil {
		log.Printf("  [ERROR] ScaNN Index creation failed: %v", err)
	} else {
		log.Println("  [SUCCESS] AlloyDB ScaNN ANN index created successfully!")
	}

	// Step 5: Query Execution & Accuracy Benchmark
	log.Println("\n--- STEP 5: Executing Semantic Similarity Queries ---")
	searchQueries := []struct {
		QueryText        string
		ExpectedCategory string
	}{
		{QueryText: "Bought printer paper, pens, and toner cartridges", ExpectedCategory: "OFFICE_EXPENSE"},
		{QueryText: "Amazon Web Services EC2 hosting invoice payment", ExpectedCategory: "HOSTING_EXPENSE"},
		{QueryText: "Customer paid invoice for custom software development", ExpectedCategory: "REVENUE"},
		{QueryText: "Team dinner and lunch meeting with client", ExpectedCategory: "MEALS_EXPENSE"},
		{QueryText: "GitHub software developer team seats license", ExpectedCategory: "SOFTWARE_EXPENSE"},
	}

	totalCorrect := 0
	for _, q := range searchQueries {
		log.Printf("\nQuery: %q (Expecting Category: %s)", q.QueryText, q.ExpectedCategory)

		var queryVec []float32
		if embedder != nil {
			queryVec, err = embedder.Embed(ctx, q.QueryText)
			if err != nil {
				log.Printf("Failed to generate query embedding: %v", err)
			}
		}

		if len(queryVec) == 0 {
			log.Println("  Skipping query (no query vector).")
			continue
		}

		searchSQL := `
		SELECT raw_text, metadata->>'category' AS category, 1 - (embedding <=> $2::toro_core.vector) AS similarity
		FROM toro_core.ase_vector_memory
		WHERE realm_id = $1 AND embedding IS NOT NULL
		ORDER BY embedding <=> $2::toro_core.vector
		LIMIT 3;`

		queryVecLit := floatsToLiteral(queryVec)
		qRows, err := pool.Query(ctx, searchSQL, testRealmID, queryVecLit)
		if err != nil {
			log.Fatalf("Search query failed: %v", err)
		}

		rank := 1
		for qRows.Next() {
			var rawText, category string
			var similarity float64
			if err := qRows.Scan(&rawText, &category, &similarity); err != nil {
				log.Printf("Scan error: %v", err)
				continue
			}
			matchStr := " "
			if category == q.ExpectedCategory {
				matchStr = " [MATCH!]"
				if rank == 1 {
					totalCorrect++
				}
			}
			log.Printf("  Rank %d: Similarity=%.4f | Category=%-16s%s | Text=%q", rank, similarity, category, matchStr, rawText)
			rank++
		}
		qRows.Close()
	}

	accuracy := (float64(totalCorrect) / float64(len(searchQueries))) * 100.0
	log.Println("\n=======================================================")
	log.Printf("Vector Search & ScaNN Index Verification Completed!")
	log.Printf("Top-1 Semantic Query Accuracy: %.1f%% (%d/%d exact matches)", accuracy, totalCorrect, len(searchQueries))
	log.Println("=======================================================")
}

func floatsToLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.Grow(len(v)*10 + 2)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%f", f)
	}
	b.WriteByte(']')
	return b.String()
}
