package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	dbURL := "postgres://toro:toro_password@127.0.0.1:5432/toro?sslmode=disable"

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, "SELECT id, tenant_id, realm_id, name FROM toro_core.ase_dags")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Found ASE DAGs:")
	for rows.Next() {
		var id, tenantID, realmID, name string
		var rawTenant, rawRealm *string
		err := rows.Scan(&id, &rawTenant, &rawRealm, &name)
		if err != nil {
			log.Fatal(err)
		}
		if rawTenant != nil {
			tenantID = *rawTenant
		} else {
			tenantID = "NULL"
		}
		if rawRealm != nil {
			realmID = *rawRealm
		} else {
			realmID = "NULL"
		}
		fmt.Printf("ID: %s, TenantID: %s, RealmID: %s, Name: %s\n", id, tenantID, realmID, name)
	}
}
