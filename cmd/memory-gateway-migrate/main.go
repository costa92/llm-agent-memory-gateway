package main

import (
	"context"
	"log"
	"os"

	"github.com/costa92/llm-agent-memory-gateway/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := os.Getenv("LLM_AGENT_MEMORY_PG_URL")
	if dsn == "" {
		log.Fatal("LLM_AGENT_MEMORY_PG_URL is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("open pgx pool: %v", err)
	}
	defer pool.Close()

	if err := service.NewPostgresGatewayMigrator(pool).Migrate(ctx); err != nil {
		log.Fatalf("run gateway migrations: %v", err)
	}
}
