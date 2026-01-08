package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"time"
)

// VectorItem represents a vector to be stored
type VectorItem struct {
	BlockID    string
	DocumentID string
	Vector     []float32
	Text       string
	Model      string
}

// VectorResult represents a vector search result
type VectorResult struct {
	BlockID    string
	DocumentID string
	Score      float32
	Distance   float32
}

// SaveVectors stores vectors for a document
func (s *Store) SaveVectors(documentID string, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO vectors (block_id, document_id, vector, text_hash, model, dimension, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)

	for _, item := range items {
		vectorBlob := vectorToBytes(item.Vector)
		textHash := hashText(item.Text)
		dimension := len(item.Vector)

		_, err := stmt.Exec(
			item.BlockID,
			documentID,
			vectorBlob,
			textHash,
			item.Model,
			dimension,
			now,
		)
		if err != nil {
			return fmt.Errorf("insert vector for %s: %w", item.BlockID, err)
		}
	}

	return tx.Commit()
}

// GetVector retrieves a single vector by block ID
func (s *Store) GetVector(documentID, blockID string) ([]float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var vectorBlob []byte
	err := s.db.QueryRow(`
		SELECT vector FROM vectors WHERE document_id = ? AND block_id = ?
	`, documentID, blockID).Scan(&vectorBlob)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("vector not found: %s/%s", documentID, blockID)
	}
	if err != nil {
		return nil, fmt.Errorf("query vector: %w", err)
	}

	return bytesToVector(vectorBlob), nil
}

// GetAllVectors retrieves all vectors (for HNSW index building)
func (s *Store) GetAllVectors() ([]VectorItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT block_id, document_id, vector, model FROM vectors
	`)
	if err != nil {
		return nil, fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	var items []VectorItem
	for rows.Next() {
		var item VectorItem
		var vectorBlob []byte

		err := rows.Scan(&item.BlockID, &item.DocumentID, &vectorBlob, &item.Model)
		if err != nil {
			return nil, fmt.Errorf("scan vector: %w", err)
		}

		item.Vector = bytesToVector(vectorBlob)
		items = append(items, item)
	}

	return items, rows.Err()
}

// GetVectorsForDocument retrieves all vectors for a document
func (s *Store) GetVectorsForDocument(documentID string) ([]VectorItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT block_id, document_id, vector, model FROM vectors WHERE document_id = ?
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	var items []VectorItem
	for rows.Next() {
		var item VectorItem
		var vectorBlob []byte

		err := rows.Scan(&item.BlockID, &item.DocumentID, &vectorBlob, &item.Model)
		if err != nil {
			return nil, fmt.Errorf("scan vector: %w", err)
		}

		item.Vector = bytesToVector(vectorBlob)
		items = append(items, item)
	}

	return items, rows.Err()
}

// DeleteVectorsForDocument removes all vectors for a document
func (s *Store) DeleteVectorsForDocument(documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM vectors WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete vectors: %w", err)
	}
	return nil
}

// GetVectorCount returns the total number of vectors
func (s *Store) GetVectorCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM vectors`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count vectors: %w", err)
	}
	return count, nil
}

// NeedsReembedding checks if a block needs re-embedding (text changed)
func (s *Store) NeedsReembedding(documentID, blockID, text string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var storedHash string
	err := s.db.QueryRow(`
		SELECT text_hash FROM vectors WHERE document_id = ? AND block_id = ?
	`, documentID, blockID).Scan(&storedHash)

	if err == sql.ErrNoRows {
		return true, nil // No vector exists
	}
	if err != nil {
		return false, fmt.Errorf("query text hash: %w", err)
	}

	currentHash := hashText(text)
	return storedHash != currentHash, nil
}

// BruteForceSearch performs brute-force nearest neighbor search
// Use this for small vector counts or as fallback when HNSW isn't available
func (s *Store) BruteForceSearch(queryVector []float32, k int, documentIDs []string) ([]VectorResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var query string
	var args []interface{}

	if len(documentIDs) > 0 {
		placeholders := make([]string, len(documentIDs))
		for i := range documentIDs {
			placeholders[i] = "?"
			args = append(args, documentIDs[i])
		}
		query = fmt.Sprintf(`
			SELECT block_id, document_id, vector FROM vectors
			WHERE document_id IN (%s)
		`, joinStrings(placeholders, ","))
	} else {
		query = `SELECT block_id, document_id, vector FROM vectors`
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	// Calculate distances
	var candidates []scored

	for rows.Next() {
		var blockID, docID string
		var vectorBlob []byte

		if err := rows.Scan(&blockID, &docID, &vectorBlob); err != nil {
			return nil, fmt.Errorf("scan vector: %w", err)
		}

		vector := bytesToVector(vectorBlob)
		distance := cosineDistance(queryVector, vector)

		candidates = append(candidates, scored{
			blockID:    blockID,
			documentID: docID,
			distance:   distance,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by distance (lower is better for cosine distance)
	sortByDistance(candidates)

	// Take top k
	if k > len(candidates) {
		k = len(candidates)
	}

	results := make([]VectorResult, k)
	for i := 0; i < k; i++ {
		results[i] = VectorResult{
			BlockID:    candidates[i].blockID,
			DocumentID: candidates[i].documentID,
			Distance:   candidates[i].distance,
			Score:      1 - candidates[i].distance, // Convert distance to similarity
		}
	}

	return results, nil
}

// vectorToBytes converts float32 slice to bytes
func vectorToBytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// bytesToVector converts bytes to float32 slice
func bytesToVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// hashText creates a hash of text for change detection
func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:8]) // Use first 8 bytes for brevity
}

// cosineDistance calculates cosine distance (1 - similarity)
func cosineDistance(a, b []float32) float32 {
	if len(a) != len(b) {
		return 1.0 // Maximum distance for incompatible vectors
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 1.0
	}

	similarity := dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
	return 1.0 - similarity
}

// scored holds a scored vector for sorting
type scored struct {
	blockID    string
	documentID string
	distance   float32
}

// sortByDistance sorts candidates by distance (ascending)
func sortByDistance(candidates []scored) {
	// Simple insertion sort - fine for small k
	for i := 1; i < len(candidates); i++ {
		key := candidates[i]
		j := i - 1
		for j >= 0 && candidates[j].distance > key.distance {
			candidates[j+1] = candidates[j]
			j--
		}
		candidates[j+1] = key
	}
}

// joinStrings joins strings with separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
