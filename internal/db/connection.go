package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func OpenFromEnv() (*sql.DB, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func EnsureSchema(ctx context.Context, db *sql.DB) error {
	const q = `
CREATE TABLE IF NOT EXISTS recipes (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	image_path TEXT NOT NULL,
	source_url TEXT NOT NULL DEFAULT '',
	ingredients_json JSONB NOT NULL,
	deleted_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`
	if _, err := db.ExecContext(ctx, q); err != nil {
		return err
	}

	const qAlterDeleted = `
ALTER TABLE recipes
ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;`
	if _, err := db.ExecContext(ctx, qAlterDeleted); err != nil {
		return err
	}

	const qAlterSource = `
ALTER TABLE recipes
ADD COLUMN IF NOT EXISTS source_url TEXT NOT NULL DEFAULT '';`
	if _, err := db.ExecContext(ctx, qAlterSource); err != nil {
		return err
	}

	const qRegular = `
CREATE TABLE IF NOT EXISTS regular_lists (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	items_json JSONB NOT NULL,
	deleted_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`
	if _, err := db.ExecContext(ctx, qRegular); err != nil {
		return err
	}

	const qRegularDeleted = `
ALTER TABLE regular_lists
ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;`
	_, err := db.ExecContext(ctx, qRegularDeleted)
	return err
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}
