package docx

import (
	"strings"
)

// RelationshipResolver resolves relationship IDs to targets
type RelationshipResolver struct {
	rels map[string]*Relationship
}

// NewRelationshipResolver creates a resolver from relationships
func NewRelationshipResolver(rels *Relationships) *RelationshipResolver {
	r := &RelationshipResolver{
		rels: make(map[string]*Relationship),
	}

	if rels != nil {
		for i := range rels.Relationships {
			rel := &rels.Relationships[i]
			r.rels[rel.ID] = rel
		}
	}

	return r
}

// GetTarget returns the target for a relationship ID
func (r *RelationshipResolver) GetTarget(rID string) string {
	if rel, ok := r.rels[rID]; ok {
		return rel.Target
	}
	return ""
}

// GetRelationship returns the full relationship for an ID
func (r *RelationshipResolver) GetRelationship(rID string) *Relationship {
	return r.rels[rID]
}

// GetImagePath returns the image path for a relationship ID
// Returns empty string if the relationship is not an image
func (r *RelationshipResolver) GetImagePath(rID string) string {
	rel := r.rels[rID]
	if rel == nil {
		return ""
	}

	if rel.Type == RelTypeImage {
		return rel.Target
	}

	return ""
}

// GetHyperlinkURL returns the hyperlink URL for a relationship ID
// Returns empty string if the relationship is not a hyperlink
func (r *RelationshipResolver) GetHyperlinkURL(rID string) string {
	rel := r.rels[rID]
	if rel == nil {
		return ""
	}

	if rel.Type == RelTypeHyperlink {
		return rel.Target
	}

	return ""
}

// IsExternalLink checks if a relationship is an external link
func (r *RelationshipResolver) IsExternalLink(rID string) bool {
	rel := r.rels[rID]
	if rel == nil {
		return false
	}
	return rel.TargetMode == "External"
}

// GetByType returns all relationships of a specific type
func (r *RelationshipResolver) GetByType(relType string) []*Relationship {
	var result []*Relationship
	for _, rel := range r.rels {
		if rel.Type == relType {
			result = append(result, rel)
		}
	}
	return result
}

// GetAllImages returns all image relationships
func (r *RelationshipResolver) GetAllImages() []*Relationship {
	return r.GetByType(RelTypeImage)
}

// GetAllHyperlinks returns all hyperlink relationships
func (r *RelationshipResolver) GetAllHyperlinks() []*Relationship {
	return r.GetByType(RelTypeHyperlink)
}

// MergeRelationships merges multiple relationship sets into one resolver
func MergeRelationships(relSets ...*Relationships) *RelationshipResolver {
	r := &RelationshipResolver{
		rels: make(map[string]*Relationship),
	}

	for _, rels := range relSets {
		if rels == nil {
			continue
		}
		for i := range rels.Relationships {
			rel := &rels.Relationships[i]
			r.rels[rel.ID] = rel
		}
	}

	return r
}

// NormalizeImagePath normalizes an image path to be relative to word/ folder
func NormalizeImagePath(target string) string {
	// Remove leading ../ if present (relative path from word/_rels/)
	target = strings.TrimPrefix(target, "../")

	// Ensure path starts with media/ or word/media/
	if strings.HasPrefix(target, "media/") {
		return "word/" + target
	}
	if strings.HasPrefix(target, "word/media/") {
		return target
	}

	// Already a full path or other format
	return target
}

// GetImageFilename extracts the filename from an image path
func GetImageFilename(target string) string {
	parts := strings.Split(target, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return target
}
