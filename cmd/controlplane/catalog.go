package main

import (
	"context"
	"log/slog"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// catalogEntry is the seed data for one starter stack. This is a small
// hand-curated catalog, not yet a browsable/versioned marketplace —
// spanning the product's three original categories (business app,
// database, AI tooling) so the marketplace concept has real, useful
// content from day one rather than a single smoke-test stack.
type catalogEntry struct {
	name        string
	description string
	category    string
	compose     string
	defaultEnv  map[string]string
	parameters  []store.Parameter
}

var starterCatalog = []catalogEntry{
	{
		name:        "nginx-hello",
		description: "A minimal nginx web server. Good for confirming end-to-end deploys work before trying something heavier.",
		category:    "web",
		compose: `services:
  web:
    image: nginx:alpine
    ports:
      - "8899:80"
    restart: unless-stopped
`,
	},
	{
		name:        "postgres-db",
		description: "A standalone Postgres database with a persistent volume, for apps that need a real datastore.",
		category:    "database",
		compose: `services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB}
    ports:
      - "55433:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    restart: unless-stopped
volumes:
  pgdata:
`,
		defaultEnv: map[string]string{
			"POSTGRES_PASSWORD": "changeme",
			"POSTGRES_DB":       "app",
		},
		parameters: []store.Parameter{
			{Key: "POSTGRES_PASSWORD", Label: "Database password", Default: "changeme", Secret: true},
			{Key: "POSTGRES_DB", Label: "Database name", Default: "app"},
		},
	},
	{
		name:        "ollama",
		description: "Local LLM runtime. Pull and run open models (llama3, etc.) via its REST API on port 11434. Models persist in a volume across redeploys.",
		category:    "ai",
		compose: `services:
  ollama:
    image: ollama/ollama:latest
    ports:
      - "11434:11434"
    volumes:
      - ollama_data:/root/.ollama
    restart: unless-stopped
volumes:
  ollama_data:
`,
	},
	{
		name:        "qdrant",
		description: "Vector database for embeddings and similarity search, exposing REST (6333) and gRPC (6334) APIs. Storage persists in a volume across redeploys.",
		category:    "ai",
		compose: `services:
  qdrant:
    image: qdrant/qdrant:latest
    ports:
      - "6333:6333"
      - "6334:6334"
    volumes:
      - qdrant_data:/qdrant/storage
    restart: unless-stopped
volumes:
  qdrant_data:
`,
	},
}

func seedCatalog(ctx context.Context, log *slog.Logger, st *store.Store) error {
	count, err := st.CountStacks(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	for _, entry := range starterCatalog {
		if _, err := st.CreateStack(ctx, entry.name, entry.description, entry.category, entry.compose, entry.defaultEnv, entry.parameters); err != nil {
			return err
		}
	}
	log.Info("seeded catalog", "entries", len(starterCatalog))
	return nil
}
