package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// ImageInfo represents image metadata stored in SQLite
type ImageInfo struct {
	ID           string
	DocumentID   string
	BlockID      string
	Format       string
	Width        int
	Height       int
	Page         int
	OriginalName string
}

// SaveImage saves an image file and its metadata
func (s *Store) SaveImage(documentID string, data []byte, format string, width, height, page int, originalName, blockID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate UUID for image
	imageID := uuid.New().String()

	// Save image file
	filename := fmt.Sprintf("%s.%s", imageID, format)
	imagePath := filepath.Join(s.imagesDir, filename)

	if err := os.WriteFile(imagePath, data, 0644); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}

	// Save metadata to database
	_, err := s.db.Exec(`
		INSERT INTO images (id, document_id, block_id, format, width, height, page, original_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, imageID, documentID, blockID, format, width, height, page, originalName)

	if err != nil {
		// Clean up file on database error
		os.Remove(imagePath)
		return "", fmt.Errorf("insert image metadata: %w", err)
	}

	return imageID, nil
}

// GetImage retrieves image data by ID
func (s *Store) GetImage(imageID string) ([]byte, *ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get metadata
	var info ImageInfo
	var blockID sql.NullString

	err := s.db.QueryRow(`
		SELECT id, document_id, block_id, format, width, height, page, original_name
		FROM images WHERE id = ?
	`, imageID).Scan(
		&info.ID,
		&info.DocumentID,
		&blockID,
		&info.Format,
		&info.Width,
		&info.Height,
		&info.Page,
		&info.OriginalName,
	)

	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("image not found: %s", imageID)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("query image: %w", err)
	}

	info.BlockID = blockID.String

	// Read image file
	filename := fmt.Sprintf("%s.%s", imageID, info.Format)
	imagePath := filepath.Join(s.imagesDir, filename)

	data, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read image file: %w", err)
	}

	return data, &info, nil
}

// GetImageInfo retrieves image metadata without the data
func (s *Store) GetImageInfo(imageID string) (*ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var info ImageInfo
	var blockID sql.NullString

	err := s.db.QueryRow(`
		SELECT id, document_id, block_id, format, width, height, page, original_name
		FROM images WHERE id = ?
	`, imageID).Scan(
		&info.ID,
		&info.DocumentID,
		&blockID,
		&info.Format,
		&info.Width,
		&info.Height,
		&info.Page,
		&info.OriginalName,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("image not found: %s", imageID)
	}
	if err != nil {
		return nil, fmt.Errorf("query image: %w", err)
	}

	info.BlockID = blockID.String
	return &info, nil
}

// GetImagesForDocument retrieves all images for a document
func (s *Store) GetImagesForDocument(documentID string) ([]ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, document_id, block_id, format, width, height, page, original_name
		FROM images WHERE document_id = ?
		ORDER BY page, id
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query images: %w", err)
	}
	defer rows.Close()

	var images []ImageInfo
	for rows.Next() {
		var info ImageInfo
		var blockID sql.NullString

		err := rows.Scan(
			&info.ID,
			&info.DocumentID,
			&blockID,
			&info.Format,
			&info.Width,
			&info.Height,
			&info.Page,
			&info.OriginalName,
		)
		if err != nil {
			return nil, fmt.Errorf("scan image: %w", err)
		}

		info.BlockID = blockID.String
		images = append(images, info)
	}

	return images, rows.Err()
}

// DeleteImage deletes an image by ID
func (s *Store) DeleteImage(imageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get format to construct filename
	var format string
	err := s.db.QueryRow(`SELECT format FROM images WHERE id = ?`, imageID).Scan(&format)
	if err == sql.ErrNoRows {
		return fmt.Errorf("image not found: %s", imageID)
	}
	if err != nil {
		return fmt.Errorf("query image format: %w", err)
	}

	// Delete from database
	_, err = s.db.Exec(`DELETE FROM images WHERE id = ?`, imageID)
	if err != nil {
		return fmt.Errorf("delete image metadata: %w", err)
	}

	// Delete file
	filename := fmt.Sprintf("%s.%s", imageID, format)
	imagePath := filepath.Join(s.imagesDir, filename)
	os.Remove(imagePath) // Ignore error if file doesn't exist

	return nil
}

// DeleteImagesForDocument deletes all images for a document
func (s *Store) DeleteImagesForDocument(documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get all image IDs and formats
	rows, err := s.db.Query(`SELECT id, format FROM images WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("query images: %w", err)
	}

	var filesToDelete []string
	for rows.Next() {
		var id, format string
		if err := rows.Scan(&id, &format); err != nil {
			rows.Close()
			return fmt.Errorf("scan image: %w", err)
		}
		filename := fmt.Sprintf("%s.%s", id, format)
		filesToDelete = append(filesToDelete, filepath.Join(s.imagesDir, filename))
	}
	rows.Close()

	// Delete from database
	_, err = s.db.Exec(`DELETE FROM images WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete image metadata: %w", err)
	}

	// Delete files
	for _, path := range filesToDelete {
		os.Remove(path) // Ignore errors
	}

	return nil
}

// GetImageCount returns the total number of images
func (s *Store) GetImageCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM images`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count images: %w", err)
	}
	return count, nil
}

// GetImagePath returns the filesystem path for an image
func (s *Store) GetImagePath(imageID, format string) string {
	filename := fmt.Sprintf("%s.%s", imageID, format)
	return filepath.Join(s.imagesDir, filename)
}

// GetImagesBySection retrieves all images for a document in a specific section
func (s *Store) GetImagesBySection(documentID, section string) ([]ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT i.id, i.document_id, i.block_id, i.format, i.width, i.height, i.page, i.original_name
		FROM images i
		JOIN content_blocks cb ON i.block_id = cb.id AND i.document_id = cb.document_id
		WHERE i.document_id = ? AND cb.section = ?
		ORDER BY i.page, i.id
	`, documentID, section)
	if err != nil {
		return nil, fmt.Errorf("query images by section: %w", err)
	}
	defer rows.Close()

	var images []ImageInfo
	for rows.Next() {
		var info ImageInfo
		var blockID sql.NullString

		err := rows.Scan(
			&info.ID,
			&info.DocumentID,
			&blockID,
			&info.Format,
			&info.Width,
			&info.Height,
			&info.Page,
			&info.OriginalName,
		)
		if err != nil {
			return nil, fmt.Errorf("scan image: %w", err)
		}

		info.BlockID = blockID.String
		images = append(images, info)
	}

	return images, rows.Err()
}

// GetImagesByPage retrieves all images for a document on a specific page
func (s *Store) GetImagesByPage(documentID string, page int) ([]ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, document_id, block_id, format, width, height, page, original_name
		FROM images WHERE document_id = ? AND page = ?
		ORDER BY id
	`, documentID, page)
	if err != nil {
		return nil, fmt.Errorf("query images by page: %w", err)
	}
	defer rows.Close()

	var images []ImageInfo
	for rows.Next() {
		var info ImageInfo
		var blockID sql.NullString

		err := rows.Scan(
			&info.ID,
			&info.DocumentID,
			&blockID,
			&info.Format,
			&info.Width,
			&info.Height,
			&info.Page,
			&info.OriginalName,
		)
		if err != nil {
			return nil, fmt.Errorf("scan image: %w", err)
		}

		info.BlockID = blockID.String
		images = append(images, info)
	}

	return images, rows.Err()
}
