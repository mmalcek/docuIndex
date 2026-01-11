package pdf

import (
	"bytes"
	"compress/zlib"
	"strings"
	"testing"
)

func TestDecodeASCIIHex(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
		wantErr  bool
	}{
		{
			name:     "simple hex",
			input:    []byte("48656C6C6F>"),
			expected: []byte("Hello"),
		},
		{
			name:     "lowercase hex",
			input:    []byte("48656c6c6f>"),
			expected: []byte("Hello"),
		},
		{
			name:     "with whitespace",
			input:    []byte("48 65 6C 6C 6F>"),
			expected: []byte("Hello"),
		},
		{
			name:     "odd length padded",
			input:    []byte("48656C6C6F4>"),
			expected: []byte("Hello@"),
		},
		{
			name:     "empty",
			input:    []byte(">"),
			expected: []byte(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := decodeASCIIHex(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeASCIIHex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("decodeASCIIHex() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDecodeASCII85(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
		wantErr  bool
	}{
		{
			name:     "with markers",
			input:    []byte("<~87cURD]j7BEbo80~>"),
			expected: []byte("Hello world!"),
		},
		{
			name:     "without markers",
			input:    []byte("87cURD]j7BEbo80"),
			expected: []byte("Hello world!"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := decodeASCII85(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeASCII85() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("decodeASCII85() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDecodeFlateDecode(t *testing.T) {
	// Compress test data
	testData := []byte("Hello, this is a test of FlateDecode compression!")

	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(testData)
	w.Close()

	result, err := decodeFlateDecode(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("decodeFlateDecode() error = %v", err)
	}

	if !bytes.Equal(result, testData) {
		t.Errorf("decodeFlateDecode() = %q, want %q", result, testData)
	}
}

func TestDecodeFlateDecodeInvalid(t *testing.T) {
	// Invalid zlib data
	_, err := decodeFlateDecode([]byte("not valid zlib data"), nil)
	if err == nil {
		t.Error("decodeFlateDecode() expected error for invalid data")
	}
}

func TestDecodeRunLength(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
		wantErr  bool
	}{
		{
			name:     "literal copy",
			input:    []byte{4, 'H', 'e', 'l', 'l', 'o', 128}, // 5 bytes literal + EOD
			expected: []byte("Hello"),
		},
		{
			name:     "repeat byte",
			input:    []byte{254, 'A', 128}, // Repeat 'A' 3 times (257-254=3) + EOD
			expected: []byte("AAA"),
		},
		{
			name:     "mixed",
			input:    []byte{2, 'H', 'i', '!', 253, '-', 128}, // 3 literal + 4 repeats + EOD
			expected: []byte("Hi!----"),
		},
		{
			name:     "empty with EOD",
			input:    []byte{128},
			expected: []byte(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := decodeRunLength(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeRunLength() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("decodeRunLength() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDecodeRunLengthErrors(t *testing.T) {
	// Unexpected end of data during literal copy
	_, err := decodeRunLength([]byte{5, 'H', 'i'}) // Claims 6 bytes but only 2
	if err == nil {
		t.Error("Expected error for truncated literal data")
	}

	// Unexpected end of data during repeat
	_, err = decodeRunLength([]byte{254}) // Repeat but no byte to repeat
	if err == nil {
		t.Error("Expected error for truncated repeat data")
	}
}

func TestApplyPredictorInvalidParams(t *testing.T) {
	data := make([]byte, 100)

	// Invalid columns
	_, err := applyPredictor(data, Dict{
		Name("Predictor"): Integer(12),
		Name("Columns"):   Integer(MaxPredictorColumns + 1),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid predictor columns") {
		t.Error("Expected error for columns exceeding limit")
	}

	// Invalid colors
	_, err = applyPredictor(data, Dict{
		Name("Predictor"): Integer(12),
		Name("Colors"):    Integer(MaxPredictorColors + 1),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid predictor colors") {
		t.Error("Expected error for colors exceeding limit")
	}

	// Invalid bits per component
	_, err = applyPredictor(data, Dict{
		Name("Predictor"):        Integer(12),
		Name("BitsPerComponent"): Integer(MaxPredictorBits + 1),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid predictor bits") {
		t.Error("Expected error for bits per component exceeding limit")
	}
}

func TestApplyPredictorNoOp(t *testing.T) {
	data := []byte("test data")

	// Predictor 1 should return data unchanged
	result, err := applyPredictor(data, Dict{
		Name("Predictor"): Integer(1),
	})
	if err != nil {
		t.Fatalf("applyPredictor() error = %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("Predictor 1 should return data unchanged")
	}
}

func TestApplyFilterUnknown(t *testing.T) {
	_, err := applyFilter("UnknownFilter", []byte("data"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown filter") {
		t.Error("Expected error for unknown filter")
	}
}

func TestApplyFilterPassthrough(t *testing.T) {
	data := []byte("jpeg image data")

	// DCTDecode should pass through unchanged
	result, err := applyFilter("DCTDecode", data, nil)
	if err != nil {
		t.Fatalf("DCTDecode error = %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("DCTDecode should pass through data unchanged")
	}

	// JPXDecode should pass through unchanged
	result, err = applyFilter("JPXDecode", data, nil)
	if err != nil {
		t.Fatalf("JPXDecode error = %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("JPXDecode should pass through data unchanged")
	}
}

func TestApplyFilterNotImplemented(t *testing.T) {
	// JBIG2Decode not implemented
	_, err := applyFilter("JBIG2Decode", []byte("data"), nil)
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Error("Expected 'not implemented' error for JBIG2Decode")
	}

	// Crypt not implemented
	_, err = applyFilter("Crypt", []byte("data"), nil)
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Error("Expected 'not implemented' error for Crypt")
	}
}

func TestEncodeStream(t *testing.T) {
	testData := []byte("Test data for encoding")

	encoded, err := EncodeStream(testData, "FlateDecode")
	if err != nil {
		t.Fatalf("EncodeStream() error = %v", err)
	}

	// Verify by decoding
	decoded, err := decodeFlateDecode(encoded, nil)
	if err != nil {
		t.Fatalf("Decode after encode error = %v", err)
	}

	if !bytes.Equal(decoded, testData) {
		t.Error("Round-trip encode/decode failed")
	}
}

func TestEncodeStreamUnsupported(t *testing.T) {
	_, err := EncodeStream([]byte("data"), "ASCIIHexDecode")
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Error("Expected 'not implemented' error for unsupported encoding")
	}
}

func TestPaeth(t *testing.T) {
	tests := []struct {
		a, b, c  byte
		expected byte
	}{
		{0, 0, 0, 0},
		{10, 0, 0, 10},   // pa=10, pb=10, pc=10, returns a
		{0, 10, 0, 10},   // pa=10, pb=10, pc=10, returns a (tie goes to a)
		{5, 10, 5, 10},   // p=10, pa=5, pb=0, pc=5, returns b
		{10, 5, 10, 5},   // p=5, pa=5, pb=0, pc=5, returns b
		{100, 100, 100, 100}, // All equal
	}

	for i, tt := range tests {
		result := paeth(tt.a, tt.b, tt.c)
		if result != tt.expected {
			t.Errorf("test %d: paeth(%d, %d, %d) = %d, want %d",
				i, tt.a, tt.b, tt.c, result, tt.expected)
		}
	}
}

func TestAbs(t *testing.T) {
	if abs(-5) != 5 {
		t.Error("abs(-5) should be 5")
	}
	if abs(5) != 5 {
		t.Error("abs(5) should be 5")
	}
	if abs(0) != 0 {
		t.Error("abs(0) should be 0")
	}
}

func TestApplyTIFFPredictor(t *testing.T) {
	// Simple case: 2 rows, 4 bytes each, 1 byte per pixel
	// After TIFF predictor, each byte adds to previous
	data := []byte{
		1, 2, 3, 4, // Row 1: 1, 1+2=3, 3+3=6, 6+4=10 after decoding? No - input is already delta
		5, 6, 7, 8, // Row 2
	}

	result, err := applyTIFFPredictor(data, 4, 1)
	if err != nil {
		t.Fatalf("applyTIFFPredictor() error = %v", err)
	}

	// First pixel unchanged, subsequent pixels add previous
	// Row 1: 1, 1+2=3, 3+3=6, 6+4=10
	// Row 2: 5, 5+6=11, 11+7=18, 18+8=26
	expected := []byte{1, 3, 6, 10, 5, 11, 18, 26}
	if !bytes.Equal(result, expected) {
		t.Errorf("applyTIFFPredictor() = %v, want %v", result, expected)
	}
}

func TestApplyTIFFPredictorZeroBytesPerRow(t *testing.T) {
	data := []byte{1, 2, 3}
	result, err := applyTIFFPredictor(data, 0, 1)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("Zero bytes per row should return data unchanged")
	}
}

func TestMaxDecompressedSizeConstant(t *testing.T) {
	// Verify the constant is set to 500MB
	expected := 500 * 1024 * 1024
	if MaxDecompressedSize != expected {
		t.Errorf("MaxDecompressedSize = %d, want %d", MaxDecompressedSize, expected)
	}
}

func TestMaxPredictorConstants(t *testing.T) {
	if MaxPredictorColumns != 100000 {
		t.Errorf("MaxPredictorColumns = %d, want 100000", MaxPredictorColumns)
	}
	if MaxPredictorColors != 100 {
		t.Errorf("MaxPredictorColors = %d, want 100", MaxPredictorColors)
	}
	if MaxPredictorBits != 32 {
		t.Errorf("MaxPredictorBits = %d, want 32", MaxPredictorBits)
	}
}

func TestFilterAliases(t *testing.T) {
	// Test short filter name aliases
	testData := []byte("test")

	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(testData)
	w.Close()

	// "Fl" is alias for FlateDecode
	result, err := applyFilter("Fl", buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("Fl alias error = %v", err)
	}
	if !bytes.Equal(result, testData) {
		t.Error("Fl alias should work like FlateDecode")
	}

	// "AHx" is alias for ASCIIHexDecode
	result, err = applyFilter("AHx", []byte("48656C6C6F>"), nil)
	if err != nil {
		t.Fatalf("AHx alias error = %v", err)
	}
	if !bytes.Equal(result, []byte("Hello")) {
		t.Error("AHx alias should work like ASCIIHexDecode")
	}

	// "DCT" is alias for DCTDecode
	result, err = applyFilter("DCT", testData, nil)
	if err != nil {
		t.Fatalf("DCT alias error = %v", err)
	}
	if !bytes.Equal(result, testData) {
		t.Error("DCT alias should work like DCTDecode")
	}

	// "RL" is alias for RunLengthDecode
	result, err = applyFilter("RL", []byte{2, 'H', 'i', '!', 128}, nil)
	if err != nil {
		t.Fatalf("RL alias error = %v", err)
	}
	if !bytes.Equal(result, []byte("Hi!")) {
		t.Error("RL alias should work like RunLengthDecode")
	}
}
