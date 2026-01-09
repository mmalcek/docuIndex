package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

const currentSchemaVersion = 4

// Schema SQL statements
const schemaSQL = `
-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL,
    library_version TEXT DEFAULT ''
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
    content_hash TEXT DEFAULT '',
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
    description TEXT DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_documents_checksum ON documents(checksum) WHERE checksum != '';
CREATE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(content_hash) WHERE content_hash != '';
`

// initSchema creates the database schema
func initSchema(db *sql.DB, libraryVersion string) error {
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
		if err := setSchemaVersion(db, currentSchemaVersion, libraryVersion); err != nil {
			return err
		}
	} else if version < currentSchemaVersion {
		// Run migrations
		if err := runMigrations(db, version, libraryVersion); err != nil {
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

// DatabaseInfo contains information about the database schema
type DatabaseInfo struct {
	SchemaVersion  int
	LibraryVersion string
	CreatedAt      time.Time
	LastMigration  time.Time
}

// GetDatabaseInfo returns information about the database schema and version
func (s *Store) GetDatabaseInfo() (*DatabaseInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info := &DatabaseInfo{}

	// Get current schema version and library version from most recent migration
	err := s.db.QueryRow(`
		SELECT version, applied_at, COALESCE(library_version, '')
		FROM schema_version
		ORDER BY version DESC
		LIMIT 1
	`).Scan(&info.SchemaVersion, &info.LastMigration, &info.LibraryVersion)
	if err != nil {
		return nil, fmt.Errorf("get schema version: %w", err)
	}

	// Get creation time from first schema version entry
	err = s.db.QueryRow(`
		SELECT applied_at
		FROM schema_version
		ORDER BY version ASC
		LIMIT 1
	`).Scan(&info.CreatedAt)
	if err != nil {
		// If we can't get creation time, use last migration time
		info.CreatedAt = info.LastMigration
	}

	return info, nil
}

// setSchemaVersion records a schema version with library version
func setSchemaVersion(db *sql.DB, version int, libraryVersion string) error {
	_, err := db.Exec(`
		INSERT INTO schema_version (version, applied_at, library_version) VALUES (?, ?, ?)
	`, version, time.Now().UTC().Format(time.RFC3339), libraryVersion)

	if err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}

	return nil
}

// runMigrations runs schema migrations from oldVersion to currentSchemaVersion
func runMigrations(db *sql.DB, oldVersion int, libraryVersion string) error {
	// Migration functions by version
	migrations := map[int]func(*sql.DB) error{
		2: migrateV1ToV2,
		3: migrateV2ToV3,
		4: migrateV3ToV4,
	}

	for v := oldVersion + 1; v <= currentSchemaVersion; v++ {
		if migrateFn, ok := migrations[v]; ok {
			if err := migrateFn(db); err != nil {
				return fmt.Errorf("migration to v%d: %w", v, err)
			}
		}

		if err := setSchemaVersion(db, v, libraryVersion); err != nil {
			return err
		}
	}

	return nil
}

// migrateV1ToV2 adds content_hash column for deduplication
func migrateV1ToV2(db *sql.DB) error {
	// Add content_hash column
	_, err := db.Exec(`ALTER TABLE documents ADD COLUMN content_hash TEXT DEFAULT ''`)
	if err != nil {
		return fmt.Errorf("add content_hash column: %w", err)
	}

	// Create index for content_hash
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(content_hash) WHERE content_hash != ''`)
	if err != nil {
		return fmt.Errorf("create content_hash index: %w", err)
	}

	// Create index for checksum if not exists
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_checksum ON documents(checksum) WHERE checksum != ''`)
	if err != nil {
		return fmt.Errorf("create checksum index: %w", err)
	}

	return nil
}

// migrateV2ToV3 adds description column to images table for AI-friendly alt text
func migrateV2ToV3(db *sql.DB) error {
	// Add description column to images table
	_, err := db.Exec(`ALTER TABLE images ADD COLUMN description TEXT DEFAULT ''`)
	if err != nil {
		return fmt.Errorf("add description column to images: %w", err)
	}

	return nil
}

// migrateV3ToV4 adds library_version column to schema_version table
func migrateV3ToV4(db *sql.DB) error {
	// Add library_version column to schema_version table
	_, err := db.Exec(`ALTER TABLE schema_version ADD COLUMN library_version TEXT DEFAULT ''`)
	if err != nil {
		return fmt.Errorf("add library_version column to schema_version: %w", err)
	}

	return nil
}
