package docx

import (
	"strconv"
	"strings"
)

// EMU to points conversion (914400 EMUs = 1 inch, 72 points = 1 inch)
const emuPerPoint = 914400.0 / 72.0

// ImageExtractor extracts images from DOCX documents
type ImageExtractor struct {
	doc  *Document
	rels *RelationshipResolver
}

// NewImageExtractor creates a new image extractor
func NewImageExtractor(doc *Document) (*ImageExtractor, error) {
	docRels, err := doc.GetDocumentRelationships()
	if err != nil {
		return nil, err
	}

	return &ImageExtractor{
		doc:  doc,
		rels: NewRelationshipResolver(docRels),
	}, nil
}

// ExtractAllImages extracts all images from the document
func (ie *ImageExtractor) ExtractAllImages() ([]ExtractedImage, error) {
	// Get all image relationships
	imageRels := ie.rels.GetAllImages()

	var images []ExtractedImage

	for _, rel := range imageRels {
		target := rel.Target

		// Normalize path
		imagePath := NormalizeImagePath(target)

		// Read image data
		data, err := ie.doc.ReadImage(imagePath)
		if err != nil {
			// Skip images we can't read
			continue
		}

		filename := GetImageFilename(target)
		format := GetImageFormat(filename)

		img := ExtractedImage{
			Name:   filename,
			Format: format,
			Data:   data,
			RelID:  rel.ID,
		}

		images = append(images, img)
	}

	return images, nil
}

// ExtractFromDrawing extracts image info from a drawing element
func (ie *ImageExtractor) ExtractFromDrawing(drawing *Drawing) *ExtractedImage {
	if drawing == nil {
		return nil
	}

	var relID string
	var extent *Extent
	var docPr *DocPr

	// Check inline first
	if drawing.Inline != nil {
		extent = drawing.Inline.Extent
		docPr = drawing.Inline.DocPr

		if drawing.Inline.Graphic != nil &&
			drawing.Inline.Graphic.GraphicData != nil &&
			drawing.Inline.Graphic.GraphicData.Pic != nil &&
			drawing.Inline.Graphic.GraphicData.Pic.BlipFill != nil &&
			drawing.Inline.Graphic.GraphicData.Pic.BlipFill.Blip != nil {
			relID = drawing.Inline.Graphic.GraphicData.Pic.BlipFill.Blip.Embed
		}
	}

	// Check anchor if no inline
	if relID == "" && drawing.Anchor != nil {
		extent = drawing.Anchor.Extent
		docPr = drawing.Anchor.DocPr

		if drawing.Anchor.Graphic != nil &&
			drawing.Anchor.Graphic.GraphicData != nil &&
			drawing.Anchor.Graphic.GraphicData.Pic != nil &&
			drawing.Anchor.Graphic.GraphicData.Pic.BlipFill != nil &&
			drawing.Anchor.Graphic.GraphicData.Pic.BlipFill.Blip != nil {
			relID = drawing.Anchor.Graphic.GraphicData.Pic.BlipFill.Blip.Embed
		}
	}

	if relID == "" {
		return nil
	}

	// Get image target from relationship
	target := ie.rels.GetImagePath(relID)
	if target == "" {
		return nil
	}

	// Normalize path
	imagePath := NormalizeImagePath(target)

	// Read image data
	data, err := ie.doc.ReadImage(imagePath)
	if err != nil {
		return nil
	}

	filename := GetImageFilename(target)
	format := GetImageFormat(filename)

	img := &ExtractedImage{
		Name:   filename,
		Format: format,
		Data:   data,
		RelID:  relID,
	}

	// Extract dimensions from extent
	if extent != nil {
		img.Width = emuToPoints(extent.CX)
		img.Height = emuToPoints(extent.CY)
	}

	// Use document properties name if available
	if docPr != nil && docPr.Name != "" {
		img.Name = docPr.Name
	}

	return img
}

// emuToPoints converts EMUs to points
func emuToPoints(emuStr string) float64 {
	if emuStr == "" {
		return 0
	}

	emu, err := strconv.ParseFloat(emuStr, 64)
	if err != nil {
		return 0
	}

	return emu / emuPerPoint
}

// ExtractDocumentImages extracts all images from document content
func (ie *ImageExtractor) ExtractDocumentImages() ([]ExtractedImage, error) {
	doc, err := ie.doc.GetDocument()
	if err != nil {
		return nil, err
	}

	var images []ExtractedImage
	seenRelIDs := make(map[string]bool)

	// Process all paragraphs
	for _, para := range doc.Body.Paragraphs {
		for _, run := range para.Runs {
			for _, drawing := range run.Drawing {
				img := ie.ExtractFromDrawing(&drawing)
				if img != nil && !seenRelIDs[img.RelID] {
					seenRelIDs[img.RelID] = true
					images = append(images, *img)
				}
			}
		}
	}

	// Process tables
	for _, table := range doc.Body.Tables {
		for _, row := range table.Rows {
			for _, cell := range row.Cells {
				for _, para := range cell.Paragraphs {
					for _, run := range para.Runs {
						for _, drawing := range run.Drawing {
							img := ie.ExtractFromDrawing(&drawing)
							if img != nil && !seenRelIDs[img.RelID] {
								seenRelIDs[img.RelID] = true
								images = append(images, *img)
							}
						}
					}
				}
			}
		}
	}

	return images, nil
}

// GetRelationshipResolver returns the relationship resolver
func (ie *ImageExtractor) GetRelationshipResolver() *RelationshipResolver {
	return ie.rels
}

// IsSupportedImageFormat checks if an image format is supported
func IsSupportedImageFormat(format string) bool {
	format = strings.ToLower(format)
	switch format {
	case "jpeg", "jpg", "png", "gif", "bmp", "tiff", "tif":
		return true
	default:
		return false
	}
}

// IsVectorFormat checks if the format is a vector format (not directly supported)
func IsVectorFormat(format string) bool {
	format = strings.ToLower(format)
	switch format {
	case "emf", "wmf", "svg":
		return true
	default:
		return false
	}
}

// FileExtension returns the file extension for an image format
func FileExtension(format string) string {
	format = strings.ToLower(format)
	switch format {
	case "jpeg":
		return ".jpg"
	default:
		return "." + format
	}
}
