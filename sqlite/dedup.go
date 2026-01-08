package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DedupInfo contains information about a potential duplicate
type DedupInfo struct {
	IsDuplicate  bool
	ExistingID   string
	ExistingName string
	Method       string // "checksum" or "content_hash"
}

// CheckDuplicateByChecksum checks if a document with the given checksum exists
func (s *Store) CheckDuplicateByChecksum(checksum string) (*DedupInfo, error) {
	if checksum == "" {
		return &DedupInfo{IsDuplicate: false}, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var id, name string
	err := s.db.QueryRow(`
		SELECT id, name FROM documents WHERE checksum = ? LIMIT 1
	`, checksum).Scan(&id, &name)

	if err == sql.ErrNoRows {
		return &DedupInfo{IsDuplicate: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check duplicate by checksum: %w", err)
	}

	return &DedupInfo{
		IsDuplicate:  true,
		ExistingID:   id,
		ExistingName: name,
		Method:       "checksum",
	}, nil
}

// CheckDuplicateByContentHash checks if a document with the given content hash exists
func (s *Store) CheckDuplicateByContentHash(contentHash string) (*DedupInfo, error) {
	if contentHash == "" {
		return &DedupInfo{IsDuplicate: false}, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var id, name string
	err := s.db.QueryRow(`
		SELECT id, name FROM documents WHERE content_hash = ? LIMIT 1
	`, contentHash).Scan(&id, &name)

	if err == sql.ErrNoRows {
		return &DedupInfo{IsDuplicate: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check duplicate by content hash: %w", err)
	}

	return &DedupInfo{
		IsDuplicate:  true,
		ExistingID:   id,
		ExistingName: name,
		Method:       "content_hash",
	}, nil
}

// GenerateContentHash generates a hash from document text content
// The hash is based on normalized, sorted text from all blocks
func GenerateContentHash(blocks []ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}

	// Collect and normalize text from blocks
	var texts []string
	for _, block := range blocks {
		if block.Type == "image" {
			continue
		}
		text := normalizeText(block.Content)
		if text != "" {
			texts = append(texts, text)
		}
	}

	if len(texts) == 0 {
		return ""
	}

	// Sort for consistent ordering regardless of extraction order
	sort.Strings(texts)

	// Concatenate and hash
	combined := strings.Join(texts, "\n")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("sha256:%x", hash)
}

// normalizeText normalizes text for consistent hashing
func normalizeText(text string) string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Normalize whitespace
	fields := strings.Fields(text)
	text = strings.Join(fields, " ")

	// Remove common punctuation that may vary
	text = strings.Map(func(r rune) rune {
		switch r {
		case '.', ',', '!', '?', ';', ':', '"', '\'', '(', ')', '[', ']', '{', '}':
			return -1
		default:
			return r
		}
	}, text)

	return strings.TrimSpace(text)
}

// UpdateContentHash updates the content_hash for a document
func (s *Store) UpdateContentHash(documentID, contentHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE documents SET content_hash = ? WHERE id = ?
	`, contentHash, documentID)

	if err != nil {
		return fmt.Errorf("update content hash: %w", err)
	}

	return nil
}

// GetContentHash retrieves the content hash for a document
func (s *Store) GetContentHash(documentID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var contentHash string
	err := s.db.QueryRow(`
		SELECT COALESCE(content_hash, '') FROM documents WHERE id = ?
	`, documentID).Scan(&contentHash)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get content hash: %w", err)
	}

	return contentHash, nil
}

// FindSimilarDocuments finds documents that might be similar based on various criteria
func (s *Store) FindSimilarDocuments(name string, sizeBytes int64, pageCount int) ([]DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Look for documents with similar characteristics
	rows, err := s.db.Query(`
		SELECT id, name, original_path, format, size_bytes, page_count, checksum,
		       created_at, updated_at, source, description, imported_at, external_id
		FROM documents
		WHERE (
			name = ? OR
			(size_bytes = ? AND page_count = ?) OR
			(size_bytes BETWEEN ? AND ?)
		)
		LIMIT 10
	`, name, sizeBytes, pageCount, sizeBytes*95/100, sizeBytes*105/100)

	if err != nil {
		return nil, fmt.Errorf("find similar documents: %w", err)
	}
	defer rows.Close()

	var docs []DocumentInfo
	for rows.Next() {
		var doc DocumentInfo
		var createdAt, updatedAt, importedAt string
		err := rows.Scan(
			&doc.ID, &doc.Name, &doc.OriginalPath, &doc.Format,
			&doc.SizeBytes, &doc.PageCount, &doc.Checksum,
			&createdAt, &updatedAt, &doc.Source, &doc.Description,
			&importedAt, &doc.ExternalID,
		)
		if err != nil {
			continue
		}
		doc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		doc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		doc.ImportedAt, _ = time.Parse(time.RFC3339, importedAt)
		docs = append(docs, doc)
	}

	return docs, nil
}
