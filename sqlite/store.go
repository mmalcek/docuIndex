package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// Store implements document storage using SQLite
type Store struct {
	db        *sql.DB
	dataDir   string
	imagesDir string
	mu        sync.RWMutex

	// Configuration
	config *StoreConfig
}

// StoreConfig holds configuration options for the SQLite store
type StoreConfig struct {
	ExtractImages    bool
	SemanticAnalysis bool
	ComputeChecksum  bool
	UseStemming      bool
	UseStopWords     bool
}

// DefaultStoreConfig returns the default store configuration
func DefaultStoreConfig() *StoreConfig {
	return &StoreConfig{
		ExtractImages:    false,
		SemanticAnalysis: true,
		ComputeChecksum:  false,
		UseStemming:      true,
		UseStopWords:     true,
	}
}

// Option is a function that configures the store
type Option func(*StoreConfig)

// WithImageExtraction enables or disables image extraction
func WithImageExtraction(enabled bool) Option {
	return func(c *StoreConfig) {
		c.ExtractImages = enabled
	}
}

// WithSemanticAnalysis enables or disables semantic analysis
func WithSemanticAnalysis(enabled bool) Option {
	return func(c *StoreConfig) {
		c.SemanticAnalysis = enabled
	}
}

// WithChecksum enables or disables checksum computation
func WithChecksum(enabled bool) Option {
	return func(c *StoreConfig) {
		c.ComputeChecksum = enabled
	}
}

// WithStemming enables or disables Porter stemming for search
func WithStemming(enabled bool) Option {
	return func(c *StoreConfig) {
		c.UseStemming = enabled
	}
}

// WithStopWords enables or disables stop word filtering
func WithStopWords(enabled bool) Option {
	return func(c *StoreConfig) {
		c.UseStopWords = enabled
	}
}

// NewStore creates a new SQLite-backed document store
func NewStore(dataDir string, libraryVersion string, opts ...Option) (*Store, error) {
	// Apply options
	config := DefaultStoreConfig()
	for _, opt := range opts {
		opt(config)
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	// Create images directory
	imagesDir := filepath.Join(dataDir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return nil, fmt.Errorf("create images directory: %w", err)
	}

	// Open SQLite database
	dbPath := filepath.Join(dataDir, "docuindex.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Initialize schema
	if err := initSchema(db, libraryVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Store{
		db:        db,
		dataDir:   dataDir,
		imagesDir: imagesDir,
		config:    config,
	}, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Config returns the store configuration
func (s *Store) Config() *StoreConfig {
	return s.config
}

// DataDir returns the data directory path
func (s *Store) DataDir() string {
	return s.dataDir
}

// ImagesDir returns the images directory path
func (s *Store) ImagesDir() string {
	return s.imagesDir
}

// DB returns the underlying database connection (for advanced use)
func (s *Store) DB() *sql.DB {
	return s.db
}

// beginTx starts a new transaction with appropriate isolation
func (s *Store) beginTx() (*sql.Tx, error) {
	return s.db.Begin()
}
