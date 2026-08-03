package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := "postgres://toro:toro_password@localhost:5432/toro?sslmode=disable"
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		fmt.Println("Error connecting to DB:", err)
		os.Exit(1)
	}
	defer pool.Close()

	tag, err := pool.Exec(context.Background(), "UPDATE toro_core.conversation_sessions SET context_json = '{}' WHERE context_json::text LIKE '%ocr_extractions%';")
	if err != nil {
		fmt.Println("Error wiping cache:", err)
		os.Exit(1)
	}
	fmt.Printf("Successfully wiped OCR cache for %d sessions!\n", tag.RowsAffected())
}
