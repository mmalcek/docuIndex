package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SaveDocumentTags saves tags for a document
func (s *Store) SaveDocumentTags(documentID string, tags map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing tags
	_, err = tx.Exec(`DELETE FROM document_tags WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete old tags: %w", err)
	}

	// Insert new tags
	if len(tags) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO document_tags (document_id, tag_key, tag_value) VALUES (?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("prepare tag insert: %w", err)
		}
		defer stmt.Close()

		for key, value := range tags {
			_, err := stmt.Exec(documentID, key, value)
			if err != nil {
				return fmt.Errorf("insert tag %s: %w", key, err)
			}
		}
	}

	return tx.Commit()
}

// GetDocumentTags retrieves tags for a document
func (s *Store) GetDocumentTags(documentID string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT tag_key, tag_value FROM document_tags WHERE document_id = ?
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()

	tags := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags[key] = value
	}

	return tags, rows.Err()
}

// GetDocumentIDsByTags returns document IDs matching all specified tags (AND logic)
func (s *Store) GetDocumentIDsByTags(tags map[string]string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(tags) == 0 {
		return nil, nil
	}

	// Build query to match ALL tags using INTERSECT
	var queries []string
	var args []interface{}

	for key, value := range tags {
		queries = append(queries, `SELECT document_id FROM document_tags WHERE tag_key = ? AND tag_value = ?`)
		args = append(args, key, value)
	}

	query := strings.Join(queries, " INTERSECT ")

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query documents by tags: %w", err)
	}
	defer rows.Close()

	var docIDs []string
	for rows.Next() {
		var docID string
		if err := rows.Scan(&docID); err != nil {
			return nil, fmt.Errorf("scan document id: %w", err)
		}
		docIDs = append(docIDs, docID)
	}

	return docIDs, rows.Err()
}

// GetDocumentIDsBySources returns document IDs matching any of the specified sources
func (s *Store) GetDocumentIDsBySources(sources []string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(sources) == 0 {
		return nil, nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(sources))
	args := make([]interface{}, len(sources))
	for i, src := range sources {
		placeholders[i] = "?"
		args[i] = src
	}

	query := fmt.Sprintf(`
		SELECT id FROM documents WHERE source IN (%s) OR format IN (%s)
	`, strings.Join(placeholders, ","), strings.Join(placeholders, ","))

	// Duplicate args for both IN clauses
	allArgs := append(args, args...)

	rows, err := s.db.Query(query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("query documents by sources: %w", err)
	}
	defer rows.Close()

	var docIDs []string
	for rows.Next() {
		var docID string
		if err := rows.Scan(&docID); err != nil {
			return nil, fmt.Errorf("scan document id: %w", err)
		}
		docIDs = append(docIDs, docID)
	}

	return docIDs, rows.Err()
}

// DeleteDocumentTags removes all tags for a document
func (s *Store) DeleteDocumentTags(documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM document_tags WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete tags: %w", err)
	}
	return nil
}

// GetLastImportTime returns the most recent import timestamp for a given source
// Returns zero time if no imports found for the source
func (s *Store) GetLastImportTime(source string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var importedAt string
	err := s.db.QueryRow(`
		SELECT imported_at FROM documents
		WHERE source = ? AND imported_at != ''
		ORDER BY imported_at DESC LIMIT 1
	`, source).Scan(&importedAt)

	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("query last import time: %w", err)
	}

	t, err := time.Parse(time.RFC3339, importedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse import time: %w", err)
	}

	return t, nil
}
