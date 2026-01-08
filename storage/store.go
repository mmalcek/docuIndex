package storage

// Store defines the interface for document storage
type Store interface {
	// Document operations
	SaveDocument(doc *Document) error
	GetDocument(id string) (*Document, error)
	DeleteDocument(id string) error
	ListDocuments() ([]*DocumentInfo, error)
	DocumentExists(id string) bool

	// Image operations
	SaveImage(docID, imageID string, data []byte, format string) error
	GetImage(docID, imageID string) ([]byte, error)
	ListImages(docID string) ([]string, error)

	// Index operations
	SaveIndex(data []byte) error
	GetIndex() ([]byte, error)

	// Utility
	GetBasePath() string
	Close() error
}
