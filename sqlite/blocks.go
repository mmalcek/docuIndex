package sqlite

import (
	"database/sql"
	"fmt"
)

// ContentBlock represents a content block stored in SQLite
type ContentBlock struct {
	ID           string
	Type         string
	Content      string
	Page         int
	Sequence     int
	BBoxX        float64
	BBoxY        float64
	BBoxWidth    float64
	BBoxHeight   float64
	PageWidth    float64
	PageHeight   float64
	FontName     string
	FontSize     float64
	FontBold     bool
	FontItalic   bool
	IsHeading    bool
	HeadingLevel int
	Section      string
	Keywords     []string
	Context      string
	Children     []string
}

// saveBlocksTx saves blocks within a transaction
func saveBlocksTx(tx *sql.Tx, documentID string, blocks []ContentBlock) error {
	stmt, err := tx.Prepare(`
		INSERT INTO content_blocks (
			id, document_id, type, content, page, sequence,
			bbox_x, bbox_y, bbox_width, bbox_height, page_width, page_height,
			font_name, font_size, font_bold, font_italic,
			is_heading, heading_level, section, keywords, context, children
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare block insert: %w", err)
	}
	defer stmt.Close()

	for i, block := range blocks {
		_, err := stmt.Exec(
			block.ID,
			documentID,
			block.Type,
			block.Content,
			block.Page,
			i, // sequence
			block.BBoxX,
			block.BBoxY,
			block.BBoxWidth,
			block.BBoxHeight,
			block.PageWidth,
			block.PageHeight,
			block.FontName,
			block.FontSize,
			boolToInt(block.FontBold),
			boolToInt(block.FontItalic),
			boolToInt(block.IsHeading),
			block.HeadingLevel,
			block.Section,
			jsonMarshal(block.Keywords),
			block.Context,
			jsonMarshal(block.Children),
		)
		if err != nil {
			return fmt.Errorf("insert block %s: %w", block.ID, err)
		}
	}

	return nil
}

// SaveBlocks saves blocks for a document (replaces existing)
func (s *Store) SaveBlocks(documentID string, blocks []ContentBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing blocks
	_, err = tx.Exec(`DELETE FROM content_blocks WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete old blocks: %w", err)
	}

	// Insert new blocks
	if err := saveBlocksTx(tx, documentID, blocks); err != nil {
		return err
	}

	return tx.Commit()
}

// GetBlocksByDocument retrieves all blocks for a document
func (s *Store) GetBlocksByDocument(documentID string) ([]ContentBlock, error) {
	rows, err := s.db.Query(`
		SELECT
			id, type, content, page, sequence,
			bbox_x, bbox_y, bbox_width, bbox_height, page_width, page_height,
			font_name, font_size, font_bold, font_italic,
			is_heading, heading_level, section, keywords, context, children
		FROM content_blocks
		WHERE document_id = ?
		ORDER BY sequence
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query blocks: %w", err)
	}
	defer rows.Close()

	return scanBlocks(rows)
}

// GetBlocksByPage retrieves blocks for a specific page
func (s *Store) GetBlocksByPage(documentID string, page int) ([]ContentBlock, error) {
	rows, err := s.db.Query(`
		SELECT
			id, type, content, page, sequence,
			bbox_x, bbox_y, bbox_width, bbox_height, page_width, page_height,
			font_name, font_size, font_bold, font_italic,
			is_heading, heading_level, section, keywords, context, children
		FROM content_blocks
		WHERE document_id = ? AND page = ?
		ORDER BY sequence
	`, documentID, page)
	if err != nil {
		return nil, fmt.Errorf("query blocks: %w", err)
	}
	defer rows.Close()

	return scanBlocks(rows)
}

// GetBlockByID retrieves a single block
func (s *Store) GetBlockByID(documentID, blockID string) (*ContentBlock, error) {
	var block ContentBlock
	var fontBold, fontItalic, isHeading int
	var keywords, children string
	var fontName sql.NullString
	var fontSizeNull sql.NullFloat64

	err := s.db.QueryRow(`
		SELECT
			id, type, content, page, sequence,
			bbox_x, bbox_y, bbox_width, bbox_height, page_width, page_height,
			font_name, font_size, font_bold, font_italic,
			is_heading, heading_level, section, keywords, context, children
		FROM content_blocks
		WHERE document_id = ? AND id = ?
	`, documentID, blockID).Scan(
		&block.ID,
		&block.Type,
		&block.Content,
		&block.Page,
		&block.Sequence,
		&block.BBoxX,
		&block.BBoxY,
		&block.BBoxWidth,
		&block.BBoxHeight,
		&block.PageWidth,
		&block.PageHeight,
		&fontName,
		&fontSizeNull,
		&fontBold,
		&fontItalic,
		&isHeading,
		&block.HeadingLevel,
		&block.Section,
		&keywords,
		&block.Context,
		&children,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("block not found: %s/%s", documentID, blockID)
	}
	if err != nil {
		return nil, fmt.Errorf("query block: %w", err)
	}

	block.FontName = fontName.String
	if fontSizeNull.Valid {
		block.FontSize = fontSizeNull.Float64
	}
	block.FontBold = fontBold != 0
	block.FontItalic = fontItalic != 0
	block.IsHeading = isHeading != 0
	jsonUnmarshal(keywords, &block.Keywords)
	jsonUnmarshal(children, &block.Children)

	return &block, nil
}

// GetBlockCount returns the total number of blocks
func (s *Store) GetBlockCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM content_blocks`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count blocks: %w", err)
	}
	return count, nil
}

// GetBlockCountForDocument returns the block count for a document
func (s *Store) GetBlockCountForDocument(documentID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM content_blocks WHERE document_id = ?`, documentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count blocks: %w", err)
	}
	return count, nil
}

// GetContextBlocks retrieves blocks around a target block for RAG
func (s *Store) GetContextBlocks(documentID, blockID string, windowSize int) (before, center, after []ContentBlock, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get the target block's sequence
	var targetSeq int
	err = s.db.QueryRow(`
		SELECT sequence FROM content_blocks WHERE document_id = ? AND id = ?
	`, documentID, blockID).Scan(&targetSeq)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("find target block: %w", err)
	}

	// Get blocks before
	beforeRows, err := s.db.Query(`
		SELECT
			id, type, content, page, sequence,
			bbox_x, bbox_y, bbox_width, bbox_height, page_width, page_height,
			font_name, font_size, font_bold, font_italic,
			is_heading, heading_level, section, keywords, context, children
		FROM content_blocks
		WHERE document_id = ? AND sequence < ? AND sequence >= ?
		ORDER BY sequence
	`, documentID, targetSeq, targetSeq-windowSize)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query before blocks: %w", err)
	}
	before, err = scanBlocks(beforeRows)
	beforeRows.Close()
	if err != nil {
		return nil, nil, nil, err
	}

	// Get center block
	centerBlock, err := s.GetBlockByID(documentID, blockID)
	if err != nil {
		return nil, nil, nil, err
	}
	center = []ContentBlock{*centerBlock}

	// Get blocks after
	afterRows, err := s.db.Query(`
		SELECT
			id, type, content, page, sequence,
			bbox_x, bbox_y, bbox_width, bbox_height, page_width, page_height,
			font_name, font_size, font_bold, font_italic,
			is_heading, heading_level, section, keywords, context, children
		FROM content_blocks
		WHERE document_id = ? AND sequence > ? AND sequence <= ?
		ORDER BY sequence
	`, documentID, targetSeq, targetSeq+windowSize)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query after blocks: %w", err)
	}
	after, err = scanBlocks(afterRows)
	afterRows.Close()
	if err != nil {
		return nil, nil, nil, err
	}

	return before, center, after, nil
}

// scanBlocks scans rows into ContentBlock slice
func scanBlocks(rows *sql.Rows) ([]ContentBlock, error) {
	var blocks []ContentBlock

	for rows.Next() {
		var block ContentBlock
		var fontBold, fontItalic, isHeading int
		var keywords, children string
		var fontName sql.NullString
		var fontSizeNull sql.NullFloat64

		err := rows.Scan(
			&block.ID,
			&block.Type,
			&block.Content,
			&block.Page,
			&block.Sequence,
			&block.BBoxX,
			&block.BBoxY,
			&block.BBoxWidth,
			&block.BBoxHeight,
			&block.PageWidth,
			&block.PageHeight,
			&fontName,
			&fontSizeNull,
			&fontBold,
			&fontItalic,
			&isHeading,
			&block.HeadingLevel,
			&block.Section,
			&keywords,
			&block.Context,
			&children,
		)
		if err != nil {
			return nil, fmt.Errorf("scan block: %w", err)
		}

		block.FontName = fontName.String
		if fontSizeNull.Valid {
			block.FontSize = fontSizeNull.Float64
		}
		block.FontBold = fontBold != 0
		block.FontItalic = fontItalic != 0
		block.IsHeading = isHeading != 0
		jsonUnmarshal(keywords, &block.Keywords)
		jsonUnmarshal(children, &block.Children)

		blocks = append(blocks, block)
	}

	return blocks, rows.Err()
}

// boolToInt converts bool to int for SQLite storage
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
