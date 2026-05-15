package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@localhost:5435/toro?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer pool.Close()

	var count int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM toro_core.workflow_blueprints").Scan(&count)
	if err != nil {
		log.Fatalf("Query failed: %v\n", err)
	}

	fmt.Printf("Total blueprints in DB: %d\n", count)

	rows, err := pool.Query(ctx, "SELECT name FROM toro_core.workflow_blueprints ORDER BY name LIMIT 20")
	if err != nil {
		log.Fatalf("Query failed: %v\n", err)
	}
	defer rows.Close()

	fmt.Println("First 20 blueprints:")
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("- %s\n", name)
	}
}
