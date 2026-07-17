package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func OpenFromEnv() (*sql.DB, error) {
	databasePath := strings.TrimSpace(os.Getenv("DATABASE_PATH"))
	if databasePath == "" {
		databasePath = "data/recipes.db"
	}

	if databasePath != ":memory:" && !strings.HasPrefix(databasePath, "file:") {
		parent := filepath.Dir(databasePath)
		if parent != "." {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

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
	ingredients_json TEXT NOT NULL,
	deleted_at TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := db.ExecContext(ctx, q); err != nil {
		return err
	}

	const qRegular = `
CREATE TABLE IF NOT EXISTS regular_lists (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	items_json TEXT NOT NULL,
	deleted_at TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := db.ExecContext(ctx, qRegular); err != nil {
		return err
	}

	return nil
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}
