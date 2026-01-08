package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

const currentSchemaVersion = 1

// Schema SQL statements
const schemaSQL = `
-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

-- Documents table
CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    original_path TEXT,
    format TEXT NOT NULL,
    size_bytes INTEGER,
    page_count INTEGER,
    checksum TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    source TEXT DEFAULT '',
    description TEXT DEFAULT '',
    imported_at TEXT DEFAULT '',
    external_id TEXT DEFAULT ''
);

-- Content blocks table
CREATE TABLE IF NOT EXISTS content_blocks (
    id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    type TEXT NOT NULL,
    content TEXT,
    page INTEGER,
    sequence INTEGER,
    bbox_x REAL,
    bbox_y REAL,
    bbox_width REAL,
    bbox_height REAL,
    page_width REAL,
    page_height REAL,
    font_name TEXT,
    font_size REAL,
    font_bold INTEGER,
    font_italic INTEGER,
    is_heading INTEGER DEFAULT 0,
    heading_level INTEGER,
    section TEXT,
    keywords TEXT,
    context TEXT,
    children TEXT,
    entry_metadata TEXT DEFAULT '',
    PRIMARY KEY (document_id, id),
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- BM25 inverted index
CREATE TABLE IF NOT EXISTS search_terms (
    term TEXT NOT NULL,
    document_id TEXT NOT NULL,
    block_id TEXT NOT NULL,
    positions TEXT,
    term_frequency REAL,
    PRIMARY KEY (term, document_id, block_id),
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- Document statistics for BM25
CREATE TABLE IF NOT EXISTS document_stats (
    document_id TEXT PRIMARY KEY,
    total_terms INTEGER,
    unique_terms INTEGER,
    avg_block_length REAL,
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- Global index statistics
CREATE TABLE IF NOT EXISTS index_stats (
    key TEXT PRIMARY KEY,
    value TEXT
);

-- Vector embeddings
CREATE TABLE IF NOT EXISTS vectors (
    block_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    vector BLOB NOT NULL,
    text_hash TEXT,
    model TEXT,
    dimension INTEGER,
    created_at TEXT NOT NULL,
    PRIMARY KEY (document_id, block_id),
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- Image references
CREATE TABLE IF NOT EXISTS images (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL,
    block_id TEXT,
    format TEXT NOT NULL,
    width INTEGER,
    height INTEGER,
    page INTEGER,
    original_name TEXT,
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- Document tags for filtering
CREATE TABLE IF NOT EXISTS document_tags (
    document_id TEXT NOT NULL,
    tag_key TEXT NOT NULL,
    tag_value TEXT NOT NULL,
    PRIMARY KEY (document_id, tag_key),
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- Indexes for query performance
CREATE INDEX IF NOT EXISTS idx_blocks_document ON content_blocks(document_id);
CREATE INDEX IF NOT EXISTS idx_blocks_page ON content_blocks(document_id, page);
CREATE INDEX IF NOT EXISTS idx_blocks_type ON content_blocks(document_id, type);
CREATE INDEX IF NOT EXISTS idx_search_term ON search_terms(term);
CREATE INDEX IF NOT EXISTS idx_search_document ON search_terms(document_id);
CREATE INDEX IF NOT EXISTS idx_vectors_document ON vectors(document_id);
CREATE INDEX IF NOT EXISTS idx_images_document ON images(document_id);
CREATE INDEX IF NOT EXISTS idx_tags_key_value ON document_tags(tag_key, tag_value);
CREATE INDEX IF NOT EXISTS idx_documents_source ON documents(source);
CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_source_external ON documents(source, external_id) WHERE external_id != '';
`

// initSchema creates the database schema
func initSchema(db *sql.DB) error {
	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}

	// Check current schema version
	version, err := getSchemaVersion(db)
	if err != nil {
		return err
	}

	if version == 0 {
		// Fresh install - create schema
		if _, err := db.Exec(schemaSQL); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}

		// Record schema version
		if err := setSchemaVersion(db, currentSchemaVersion); err != nil {
			return err
		}
	} else if version < currentSchemaVersion {
		// Run migrations
		if err := runMigrations(db, version); err != nil {
			return err
		}
	}

	return nil
}

// getSchemaVersion returns the current schema version (0 if not initialized)
func getSchemaVersion(db *sql.DB) (int, error) {
	// Check if schema_version table exists
	var tableName string
	err := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='schema_version'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("check schema_version table: %w", err)
	}

	// Get the latest version
	var version int
	err = db.QueryRow(`
		SELECT COALESCE(MAX(version), 0) FROM schema_version
	`).Scan(&version)

	if err != nil {
		return 0, fmt.Errorf("get schema version: %w", err)
	}

	return version, nil
}

// setSchemaVersion records a schema version
func setSchemaVersion(db *sql.DB, version int) error {
	_, err := db.Exec(`
		INSERT INTO schema_version (version, applied_at) VALUES (?, ?)
	`, version, time.Now().UTC().Format(time.RFC3339))

	if err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}

	return nil
}

// runMigrations runs schema migrations from oldVersion to currentSchemaVersion
func runMigrations(db *sql.DB, oldVersion int) error {
	// Migration functions by version
	migrations := map[int]func(*sql.DB) error{
		// Add migrations here as schema evolves
		// 2: migrateV1ToV2,
	}

	for v := oldVersion + 1; v <= currentSchemaVersion; v++ {
		if migrateFn, ok := migrations[v]; ok {
			if err := migrateFn(db); err != nil {
				return fmt.Errorf("migration to v%d: %w", v, err)
			}
		}

		if err := setSchemaVersion(db, v); err != nil {
			return err
		}
	}

	return nil
}
