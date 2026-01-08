package pdf

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"encoding/hex"
	"fmt"
	"io"
)

// MaxDecompressedSize limits decoded stream size to prevent memory exhaustion
const MaxDecompressedSize = 500 * 1024 * 1024 // 500MB

// MaxPredictorParams limits predictor parameters to prevent overflow
const MaxPredictorColumns = 100000
const MaxPredictorColors = 100
const MaxPredictorBits = 32

// DecodeStream decodes the stream data applying all filters
func DecodeStream(s *Stream) ([]byte, error) {
	if s.Data != nil {
		return s.Data, nil // Already decoded
	}

	data := s.RawData
	filters := s.GetFilter()

	// Get decode parameters (can be array or dict)
	decodeParams := s.Dict.Get("DecodeParms")

	for i, filter := range filters {
		var params Dict
		if decodeParams != nil {
			switch dp := decodeParams.(type) {
			case Dict:
				params = dp
			case Array:
				if i < len(dp) {
					if d, ok := dp[i].(Dict); ok {
						params = d
					}
				}
			}
		}

		var err error
		data, err = applyFilter(filter, data, params)
		if err != nil {
			return nil, fmt.Errorf("filter %s: %w", filter, err)
		}
	}

	s.Data = data
	return data, nil
}

// applyFilter applies a single filter to decode data
func applyFilter(name string, data []byte, params Dict) ([]byte, error) {
	switch name {
	case "FlateDecode", "Fl":
		return decodeFlateDecode(data, params)
	case "ASCIIHexDecode", "AHx":
		return decodeASCIIHex(data)
	case "ASCII85Decode", "A85":
		return decodeASCII85(data)
	case "LZWDecode", "LZW":
		return decodeLZW(data, params)
	case "RunLengthDecode", "RL":
		return decodeRunLength(data)
	case "DCTDecode", "DCT":
		// JPEG - return as-is, handled by image decoder
		return data, nil
	case "JPXDecode":
		// JPEG2000 - return as-is
		return data, nil
	case "CCITTFaxDecode", "CCF":
		return decodeCCITTFax(data, params)
	case "JBIG2Decode":
		// JBIG2 - not implemented yet
		return nil, fmt.Errorf("JBIG2Decode not implemented")
	case "Crypt":
		// Encryption filter - not implemented
		return nil, fmt.Errorf("Crypt filter not implemented")
	default:
		return nil, fmt.Errorf("unknown filter: %s", name)
	}
}

// decodeFlateDecode applies zlib/deflate decompression
func decodeFlateDecode(data []byte, params Dict) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zlib init: %w", err)
	}
	defer r.Close()

	decoded, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("zlib decompress: %w", err)
	}

	// Apply predictor if specified
	if params != nil {
		predictor := int(params.GetInt("Predictor"))
		if predictor > 1 {
			decoded, err = applyPredictor(decoded, params)
			if err != nil {
				return nil, fmt.Errorf("predictor: %w", err)
			}
		}
	}

	return decoded, nil
}

// applyPredictor reverses PNG/TIFF prediction
func applyPredictor(data []byte, params Dict) ([]byte, error) {
	predictor := int(params.GetInt("Predictor"))
	if predictor == 1 {
		return data, nil // No prediction
	}

	columns := int(params.GetInt("Columns"))
	if columns == 0 {
		columns = 1
	}

	colors := int(params.GetInt("Colors"))
	if colors == 0 {
		colors = 1
	}

	bitsPerComponent := int(params.GetInt("BitsPerComponent"))
	if bitsPerComponent == 0 {
		bitsPerComponent = 8
	}

	// Validate parameters to prevent overflow/memory exhaustion
	if columns < 0 || columns > MaxPredictorColumns {
		return nil, fmt.Errorf("invalid predictor columns: %d (max %d)", columns, MaxPredictorColumns)
	}
	if colors < 0 || colors > MaxPredictorColors {
		return nil, fmt.Errorf("invalid predictor colors: %d (max %d)", colors, MaxPredictorColors)
	}
	if bitsPerComponent < 1 || bitsPerComponent > MaxPredictorBits {
		return nil, fmt.Errorf("invalid predictor bits per component: %d", bitsPerComponent)
	}

	bytesPerPixel := (colors * bitsPerComponent + 7) / 8
	bytesPerRow := (columns*colors*bitsPerComponent + 7) / 8

	// Validate calculated sizes
	if bytesPerRow <= 0 || bytesPerRow > MaxDecompressedSize {
		return nil, fmt.Errorf("invalid bytes per row: %d", bytesPerRow)
	}

	if predictor == 2 {
		// TIFF predictor
		return applyTIFFPredictor(data, bytesPerRow, bytesPerPixel)
	}

	if predictor >= 10 && predictor <= 15 {
		// PNG predictors
		return applyPNGPredictor(data, bytesPerRow, bytesPerPixel)
	}

	return nil, fmt.Errorf("unsupported predictor: %d", predictor)
}

// applyTIFFPredictor applies TIFF predictor 2
func applyTIFFPredictor(data []byte, bytesPerRow, bytesPerPixel int) ([]byte, error) {
	if bytesPerRow == 0 {
		return data, nil
	}

	result := make([]byte, len(data))
	copy(result, data)

	rows := len(data) / bytesPerRow
	for row := 0; row < rows; row++ {
		rowStart := row * bytesPerRow
		for col := bytesPerPixel; col < bytesPerRow; col++ {
			result[rowStart+col] = byte(int(result[rowStart+col]) + int(result[rowStart+col-bytesPerPixel]))
		}
	}

	return result, nil
}

// applyPNGPredictor applies PNG predictors (10-15)
func applyPNGPredictor(data []byte, bytesPerRow, bytesPerPixel int) ([]byte, error) {
	// Each row has a filter byte prefix
	rowSize := bytesPerRow + 1
	if len(data)%rowSize != 0 {
		// Try without the filter byte assumption
		return applyPNGPredictorOptimized(data, bytesPerRow, bytesPerPixel)
	}

	rows := len(data) / rowSize
	result := make([]byte, 0, rows*bytesPerRow)

	prevRow := make([]byte, bytesPerRow)

	for row := 0; row < rows; row++ {
		rowStart := row * rowSize
		filterType := data[rowStart]
		rowData := data[rowStart+1 : rowStart+rowSize]

		decodedRow := make([]byte, bytesPerRow)

		for i := 0; i < bytesPerRow; i++ {
			var a, b, c byte // left, up, upper-left
			if i >= bytesPerPixel {
				a = decodedRow[i-bytesPerPixel]
			}
			b = prevRow[i]
			if i >= bytesPerPixel {
				c = prevRow[i-bytesPerPixel]
			}

			switch filterType {
			case 0: // None
				decodedRow[i] = rowData[i]
			case 1: // Sub
				decodedRow[i] = rowData[i] + a
			case 2: // Up
				decodedRow[i] = rowData[i] + b
			case 3: // Average
				decodedRow[i] = rowData[i] + byte((int(a)+int(b))/2)
			case 4: // Paeth
				decodedRow[i] = rowData[i] + paeth(a, b, c)
			default:
				return nil, fmt.Errorf("unknown PNG filter type: %d", filterType)
			}
		}

		result = append(result, decodedRow...)
		copy(prevRow, decodedRow)
	}

	return result, nil
}

// applyPNGPredictorOptimized handles case where filter byte might not be per-row
func applyPNGPredictorOptimized(data []byte, bytesPerRow, bytesPerPixel int) ([]byte, error) {
	// Assume predictor 12 (Up) without per-row filter bytes
	if bytesPerRow == 0 || len(data)%bytesPerRow != 0 {
		return data, nil // Can't apply predictor
	}

	result := make([]byte, len(data))
	rows := len(data) / bytesPerRow

	// First row: copy as-is
	copy(result[:bytesPerRow], data[:bytesPerRow])

	// Subsequent rows: add previous row
	for row := 1; row < rows; row++ {
		rowStart := row * bytesPerRow
		prevRowStart := (row - 1) * bytesPerRow
		for col := 0; col < bytesPerRow; col++ {
			result[rowStart+col] = data[rowStart+col] + result[prevRowStart+col]
		}
	}

	return result, nil
}

// paeth implements the Paeth predictor function
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa := abs(p - int(a))
	pb := abs(p - int(b))
	pc := abs(p - int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// decodeASCIIHex decodes ASCII hex encoded data
func decodeASCIIHex(data []byte) ([]byte, error) {
	// Remove whitespace and find EOD marker
	var clean bytes.Buffer
	for _, b := range data {
		if b == '>' {
			break // EOD marker
		}
		if !isWhitespace(b) {
			clean.WriteByte(b)
		}
	}

	hexStr := clean.String()
	// Pad with trailing 0 if odd length
	if len(hexStr)%2 == 1 {
		hexStr += "0"
	}

	return hex.DecodeString(hexStr)
}

// decodeASCII85 decodes ASCII85 (Base85) encoded data
func decodeASCII85(data []byte) ([]byte, error) {
	// Find start and end markers
	start := 0
	end := len(data)

	// Skip <~ prefix if present
	if len(data) >= 2 && data[0] == '<' && data[1] == '~' {
		start = 2
	}

	// Find ~> suffix
	for i := len(data) - 1; i >= start; i-- {
		if i > 0 && data[i-1] == '~' && data[i] == '>' {
			end = i - 1
			break
		}
	}

	// Remove whitespace
	var clean bytes.Buffer
	for i := start; i < end; i++ {
		if !isWhitespace(data[i]) {
			clean.WriteByte(data[i])
		}
	}

	decoder := ascii85.NewDecoder(bytes.NewReader(clean.Bytes()))
	return io.ReadAll(decoder)
}

// decodeLZW decodes LZW compressed data
func decodeLZW(data []byte, params Dict) ([]byte, error) {
	// PDF uses early change by default (like TIFF)
	earlyChange := true
	if params != nil {
		if ec := params.GetInt("EarlyChange"); ec == 0 {
			earlyChange = false
		}
	}

	// LZW in PDF uses MSB first, 8-bit literals
	order := lzw.MSB
	litWidth := 8

	// The Go lzw package doesn't support early change toggle directly
	// For now, use standard LZW
	_ = earlyChange

	r := lzw.NewReader(bytes.NewReader(data), order, litWidth)
	defer r.Close()

	decoded, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("lzw decompress: %w", err)
	}

	// Apply predictor if specified
	if params != nil {
		predictor := int(params.GetInt("Predictor"))
		if predictor > 1 {
			decoded, err = applyPredictor(decoded, params)
			if err != nil {
				return nil, fmt.Errorf("predictor: %w", err)
			}
		}
	}

	return decoded, nil
}

// decodeRunLength decodes run-length encoded data
func decodeRunLength(data []byte) ([]byte, error) {
	var result bytes.Buffer
	i := 0

	for i < len(data) {
		// Prevent unbounded memory growth
		if result.Len() > MaxDecompressedSize {
			return nil, fmt.Errorf("runlength: output exceeds maximum size of %d bytes", MaxDecompressedSize)
		}

		length := int(data[i])
		i++

		if length == 128 {
			// EOD marker
			break
		}

		if length < 128 {
			// Copy next length+1 bytes literally
			count := length + 1
			if i+count > len(data) {
				return nil, fmt.Errorf("runlength: unexpected end of data")
			}
			result.Write(data[i : i+count])
			i += count
		} else {
			// Repeat next byte (257-length) times
			count := 257 - length
			if i >= len(data) {
				return nil, fmt.Errorf("runlength: unexpected end of data")
			}
			b := data[i]
			i++
			for j := 0; j < count; j++ {
				result.WriteByte(b)
			}
		}
	}

	return result.Bytes(), nil
}

// decodeCCITTFax decodes CCITT fax compressed data (Group 3/4)
// This is a placeholder - full implementation requires significant code
func decodeCCITTFax(data []byte, params Dict) ([]byte, error) {
	// CCITT fax decoding is complex and typically used for bi-level images
	// For now, return an error indicating it's not fully implemented
	// A full implementation would decode Group 3 (1D/2D) and Group 4 fax encoding

	k := int(params.GetInt("K"))
	columns := int(params.GetInt("Columns"))
	rows := int(params.GetInt("Rows"))

	_ = k
	_ = columns
	_ = rows

	// TODO: Implement CCITT fax decoding
	// For now, this is a stub that returns the raw data
	// Real implementation would need to decode the fax encoding

	return nil, fmt.Errorf("CCITTFaxDecode not fully implemented")
}

// EncodeStream encodes data with the specified filter (for testing/future use)
func EncodeStream(data []byte, filter string) ([]byte, error) {
	switch filter {
	case "FlateDecode":
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		_, err := w.Write(data)
		if err != nil {
			return nil, err
		}
		err = w.Close()
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("encoding not implemented for: %s", filter)
	}
}
