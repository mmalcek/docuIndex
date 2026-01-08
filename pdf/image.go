package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
)

// ExtractedImage represents an extracted image
type ExtractedImage struct {
	Name       string  // XObject name
	Width      int     // Image width in pixels
	Height     int     // Image height in pixels
	BitsPerComponent int
	ColorSpace string  // DeviceRGB, DeviceGray, DeviceCMYK, etc.
	Filter     string  // DCTDecode, FlateDecode, etc.
	Data       []byte  // Raw image data
	Format     string  // Output format: jpeg, png
	Page       int
	X, Y       float64 // Position on page
	DisplayW   float64 // Display width
	DisplayH   float64 // Display height
}

// ImageExtractor extracts images from PDF pages
type ImageExtractor struct {
	doc *Document
}

// NewImageExtractor creates a new image extractor
func NewImageExtractor(doc *Document) *ImageExtractor {
	return &ImageExtractor{doc: doc}
}

// ExtractPageImages extracts all images from a page
func (ie *ImageExtractor) ExtractPageImages(pageNum int) ([]ExtractedImage, error) {
	page, err := ie.doc.GetPage(pageNum)
	if err != nil {
		return nil, err
	}

	images, err := page.GetImages()
	if err != nil {
		return nil, err
	}

	var result []ExtractedImage
	for name, stream := range images {
		img, err := ie.extractImage(name, stream, pageNum)
		if err != nil {
			// Skip problematic images
			continue
		}
		result = append(result, img)
	}

	return result, nil
}

// extractImage extracts a single image from a stream
func (ie *ImageExtractor) extractImage(name string, stream *Stream, pageNum int) (ExtractedImage, error) {
	img := ExtractedImage{
		Name:   name,
		Page:   pageNum,
		Width:  int(stream.Dict.GetInt("Width")),
		Height: int(stream.Dict.GetInt("Height")),
		BitsPerComponent: int(stream.Dict.GetInt("BitsPerComponent")),
	}

	// Get color space
	colorSpace := stream.Dict.Get("ColorSpace")
	if colorSpace != nil {
		switch cs := colorSpace.(type) {
		case Name:
			img.ColorSpace = string(cs)
		case Array:
			if len(cs) > 0 {
				if n, ok := cs[0].(Name); ok {
					img.ColorSpace = string(n)
				}
			}
		case *Ref:
			resolved, err := ie.doc.Resolve(cs)
			if err == nil {
				if n, ok := resolved.(Name); ok {
					img.ColorSpace = string(n)
				}
			}
		}
	}

	if img.ColorSpace == "" {
		img.ColorSpace = "DeviceRGB"
	}

	// Get filter
	filters := stream.GetFilter()
	if len(filters) > 0 {
		img.Filter = filters[0]
	}

	// Handle different image types
	switch img.Filter {
	case "DCTDecode", "DCT":
		// JPEG - use raw data directly
		img.Format = "jpeg"
		img.Data = stream.RawData
		return img, nil

	case "JPXDecode":
		// JPEG 2000 - use raw data
		img.Format = "jp2"
		img.Data = stream.RawData
		return img, nil

	default:
		// Decode the stream and convert to PNG
		data, err := DecodeStream(stream)
		if err != nil {
			return img, fmt.Errorf("decode stream: %w", err)
		}

		// Convert to PNG
		pngData, err := ie.convertToPNG(data, img)
		if err != nil {
			return img, fmt.Errorf("convert to PNG: %w", err)
		}

		img.Format = "png"
		img.Data = pngData
		return img, nil
	}
}

// convertToPNG converts raw image data to PNG format
func (ie *ImageExtractor) convertToPNG(data []byte, info ExtractedImage) ([]byte, error) {
	if info.Width <= 0 || info.Height <= 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}

	var img image.Image

	switch info.ColorSpace {
	case "DeviceRGB":
		img = ie.createRGBImage(data, info)
	case "DeviceGray":
		img = ie.createGrayImage(data, info)
	case "DeviceCMYK":
		img = ie.createCMYKImage(data, info)
	case "Indexed", "ICCBased":
		// Try RGB
		img = ie.createRGBImage(data, info)
	default:
		// Default to RGB
		img = ie.createRGBImage(data, info)
	}

	if img == nil {
		return nil, fmt.Errorf("failed to create image")
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// createRGBImage creates an RGB image from raw data
func (ie *ImageExtractor) createRGBImage(data []byte, info ExtractedImage) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, info.Width, info.Height))

	bytesPerPixel := 3 // RGB
	if info.BitsPerComponent != 8 {
		// Handle non-8-bit images
		return ie.createRGBImageNonStandard(data, info)
	}

	for y := 0; y < info.Height; y++ {
		for x := 0; x < info.Width; x++ {
			idx := (y*info.Width + x) * bytesPerPixel
			if idx+2 < len(data) {
				img.Set(x, y, color.RGBA{
					R: data[idx],
					G: data[idx+1],
					B: data[idx+2],
					A: 255,
				})
			}
		}
	}

	return img
}

// createRGBImageNonStandard handles non-8-bit RGB images
func (ie *ImageExtractor) createRGBImageNonStandard(data []byte, info ExtractedImage) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, info.Width, info.Height))

	// For simplicity, treat as 8-bit
	bytesPerPixel := 3
	for y := 0; y < info.Height; y++ {
		for x := 0; x < info.Width; x++ {
			idx := (y*info.Width + x) * bytesPerPixel
			if idx+2 < len(data) {
				img.Set(x, y, color.RGBA{
					R: data[idx],
					G: data[idx+1],
					B: data[idx+2],
					A: 255,
				})
			}
		}
	}

	return img
}

// createGrayImage creates a grayscale image from raw data
func (ie *ImageExtractor) createGrayImage(data []byte, info ExtractedImage) image.Image {
	img := image.NewGray(image.Rect(0, 0, info.Width, info.Height))

	if info.BitsPerComponent == 8 {
		for y := 0; y < info.Height; y++ {
			for x := 0; x < info.Width; x++ {
				idx := y*info.Width + x
				if idx < len(data) {
					img.SetGray(x, y, color.Gray{Y: data[idx]})
				}
			}
		}
	} else if info.BitsPerComponent == 1 {
		// 1-bit image (black and white)
		for y := 0; y < info.Height; y++ {
			for x := 0; x < info.Width; x++ {
				byteIdx := (y*info.Width + x) / 8
				bitIdx := 7 - ((y*info.Width + x) % 8)
				if byteIdx < len(data) {
					bit := (data[byteIdx] >> bitIdx) & 1
					if bit == 0 {
						img.SetGray(x, y, color.Gray{Y: 0})
					} else {
						img.SetGray(x, y, color.Gray{Y: 255})
					}
				}
			}
		}
	}

	return img
}

// createCMYKImage creates an image from CMYK data (converts to RGB)
func (ie *ImageExtractor) createCMYKImage(data []byte, info ExtractedImage) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, info.Width, info.Height))

	for y := 0; y < info.Height; y++ {
		for x := 0; x < info.Width; x++ {
			idx := (y*info.Width + x) * 4
			if idx+3 < len(data) {
				c := float64(data[idx]) / 255.0
				m := float64(data[idx+1]) / 255.0
				yy := float64(data[idx+2]) / 255.0
				k := float64(data[idx+3]) / 255.0

				// CMYK to RGB conversion
				r := uint8((1 - c) * (1 - k) * 255)
				g := uint8((1 - m) * (1 - k) * 255)
				b := uint8((1 - yy) * (1 - k) * 255)

				img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
			}
		}
	}

	return img
}

// SaveImage saves an extracted image to a writer
func (img *ExtractedImage) SaveImage(w io.Writer) error {
	switch img.Format {
	case "jpeg":
		_, err := w.Write(img.Data)
		return err
	case "png":
		_, err := w.Write(img.Data)
		return err
	default:
		return fmt.Errorf("unsupported format: %s", img.Format)
	}
}

// GetGoImage returns the image as a Go image.Image
func (img *ExtractedImage) GetGoImage() (image.Image, error) {
	switch img.Format {
	case "jpeg":
		return jpeg.Decode(bytes.NewReader(img.Data))
	case "png":
		return png.Decode(bytes.NewReader(img.Data))
	default:
		return nil, fmt.Errorf("unsupported format: %s", img.Format)
	}
}

// FileExtension returns the appropriate file extension for the image
func (img *ExtractedImage) FileExtension() string {
	switch img.Format {
	case "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	case "jp2":
		return ".jp2"
	default:
		return ".bin"
	}
}

// MimeType returns the MIME type for the image
func (img *ExtractedImage) MimeType() string {
	switch img.Format {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "jp2":
		return "image/jp2"
	default:
		return "application/octet-stream"
	}
}

// ExtractAllImages extracts all images from the document
func (ie *ImageExtractor) ExtractAllImages() ([]ExtractedImage, error) {
	pageCount, err := ie.doc.PageCount()
	if err != nil {
		return nil, err
	}

	var allImages []ExtractedImage
	for i := 1; i <= pageCount; i++ {
		images, err := ie.ExtractPageImages(i)
		if err != nil {
			continue
		}
		allImages = append(allImages, images...)
	}

	return allImages, nil
}
