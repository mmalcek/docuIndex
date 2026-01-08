package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// DocumentInfo mirrors the main package's DocumentInfo for SQLite storage
type DocumentInfo struct {
	ID           string
	Name         string
	OriginalPath string
	SizeBytes    int64
	PageCount    int
	Format       string
	Checksum     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Document represents a stored document with its content
type Document struct {
	Info    DocumentInfo
	Blocks  []ContentBlock
	Version string
}

// SaveDocument saves a document with its blocks in a transaction
func (s *Store) SaveDocument(doc *Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert or update document
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO documents (id, name, original_path, format, size_bytes, page_count, checksum, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		doc.Info.ID,
		doc.Info.Name,
		doc.Info.OriginalPath,
		doc.Info.Format,
		doc.Info.SizeBytes,
		doc.Info.PageCount,
		doc.Info.Checksum,
		doc.Info.CreatedAt.UTC().Format(time.RFC3339),
		doc.Info.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert document: %w", err)
	}

	// Delete existing blocks (for update case)
	_, err = tx.Exec(`DELETE FROM content_blocks WHERE document_id = ?`, doc.Info.ID)
	if err != nil {
		return fmt.Errorf("delete old blocks: %w", err)
	}

	// Insert blocks
	if err := saveBlocksTx(tx, doc.Info.ID, doc.Blocks); err != nil {
		return err
	}

	return tx.Commit()
}

// GetDocument retrieves a document by ID
func (s *Store) GetDocument(id string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get document info
	var doc Document
	var createdAt, updatedAt string

	err := s.db.QueryRow(`
		SELECT id, name, original_path, format, size_bytes, page_count, checksum, created_at, updated_at
		FROM documents WHERE id = ?
	`, id).Scan(
		&doc.Info.ID,
		&doc.Info.Name,
		&doc.Info.OriginalPath,
		&doc.Info.Format,
		&doc.Info.SizeBytes,
		&doc.Info.PageCount,
		&doc.Info.Checksum,
		&createdAt,
		&updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query document: %w", err)
	}

	// Parse timestamps
	doc.Info.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	doc.Info.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	// Get blocks
	blocks, err := s.GetBlocksByDocument(id)
	if err != nil {
		return nil, err
	}
	doc.Blocks = blocks
	doc.Version = "1.0"

	return &doc, nil
}

// ListDocuments returns all document info
func (s *Store) ListDocuments() ([]DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, original_path, format, size_bytes, page_count, checksum, created_at, updated_at
		FROM documents ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query documents: %w", err)
	}
	defer rows.Close()

	var docs []DocumentInfo
	for rows.Next() {
		var doc DocumentInfo
		var createdAt, updatedAt string

		err := rows.Scan(
			&doc.ID,
			&doc.Name,
			&doc.OriginalPath,
			&doc.Format,
			&doc.SizeBytes,
			&doc.PageCount,
			&doc.Checksum,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}

		doc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		doc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

		docs = append(docs, doc)
	}

	return docs, rows.Err()
}

// DeleteDocument removes a document and all associated data
func (s *Store) DeleteDocument(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete document (cascades to blocks, vectors, images metadata due to foreign keys)
	result, err := tx.Exec(`DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("document not found: %s", id)
	}

	return tx.Commit()
}

// DocumentExists checks if a document exists
func (s *Store) DocumentExists(id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM documents WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check document: %w", err)
	}
	return count > 0, nil
}

// GetDocumentCount returns the total number of documents
func (s *Store) GetDocumentCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return count, nil
}

// UpdateDocumentTimestamp updates the updated_at timestamp
func (s *Store) UpdateDocumentTimestamp(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE documents SET updated_at = ? WHERE id = ?
	`, time.Now().UTC().Format(time.RFC3339), id)

	if err != nil {
		return fmt.Errorf("update timestamp: %w", err)
	}
	return nil
}

// jsonMarshal is a helper to marshal JSON with null handling
func jsonMarshal(v interface{}) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

// jsonUnmarshal is a helper to unmarshal JSON from a string
func jsonUnmarshal(data string, v interface{}) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), v)
}
