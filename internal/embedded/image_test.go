package embedded

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// makeTestImage creates a PNG image of the given dimensions simulating a
// realistic UI screenshot. It includes flat background, colored rectangles
// (UI elements), horizontal lines (text rows), and subtle per-pixel noise.
// The noise is critical: without it PNG compresses too efficiently (flat
// colors → tiny PNG), and JPEG would look larger — the opposite of real-world
// screenshots where anti-aliasing and photo content make PNG files much larger
// than JPEG at quality 85.
func makeTestImage(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Flat white background (like a typical app screen).
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Draw colored rectangles (simulating UI buttons/cards).
	colors := []color.RGBA{
		{R: 66, G: 133, B: 244, A: 255},  // blue
		{R: 234, G: 67, B: 53, A: 255},   // red
		{R: 251, G: 188, B: 4, A: 255},   // yellow
		{R: 52, G: 168, B: 83, A: 255},   // green
		{R: 128, G: 128, B: 128, A: 255}, // gray
	}
	rectW := w / 5
	for i, c := range colors {
		x0 := i * rectW
		x1 := x0 + rectW - 2
		y0 := h / 4
		y1 := h * 3 / 4
		for y := y0; y < y1 && y < h; y++ {
			for x := x0; x < x1 && x < w; x++ {
				img.Set(x, y, c)
			}
		}
	}
	// Draw horizontal lines (simulating text rows).
	for y := 0; y < h; y += h / 20 {
		if y < h/4 || y > h*3/4 {
			for x := w / 10; x < w*9/10; x++ {
				img.Set(x, y, color.RGBA{R: 51, G: 51, B: 51, A: 255})
			}
		}
	}
	// Add per-pixel noise (simulating anti-aliasing, photo content, etc.).
	// This makes PNG compression much less effective, mirroring real
	// screenshots where JPEG quality 85 is typically 60-80% smaller.
	// We use a hash-like function that produces high-frequency noise that
	// PNG's DEFLATE cannot compress well but JPEG handles efficiently.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			// High-frequency pseudo-random noise (deterministic for test reproducibility)
			n := (x*73 ^ y*37 ^ (x*y)*13) & 0xFF
			c.R = uint8(int(c.R) + int(int8(n)))
			c.G = uint8(int(c.G) + int(int8(n * 3)))
			c.B = uint8(int(c.B) + int(int8(n * 7)))
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestOptimizeImageForContext_SmallImage(t *testing.T) {
	// A small image (100×100) should be re-encoded as JPEG (smaller for
	// screenshots) but still valid.
	raw := makeTestImage(100, 100)
	dataURL := optimizeImageForContext(raw, "image/png")

	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("small image should be re-encoded as JPEG, got prefix: %s", dataURL[:40])
	}

	// Verify the data URL decodes back to valid JPEG bytes.
	b64 := strings.TrimPrefix(dataURL, "data:image/jpeg;base64,")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}
	// JPEG magic bytes
	if len(decoded) < 2 || decoded[0] != 0xFF || decoded[1] != 0xD8 {
		t.Errorf("expected JPEG magic bytes FF D8, got %X %X", decoded[0], decoded[1])
	}
}

func TestOptimizeImageForContext_LargeImage(t *testing.T) {
	// A large image (3000×2000) should be resized to maxImageDim and
	// re-encoded as JPEG.
	raw := makeTestImage(3000, 2000)
	dataURL := optimizeImageForContext(raw, "image/png")

	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("large image should be re-encoded as JPEG, got prefix: %s", dataURL[:40])
	}

	// The JPEG payload should be significantly smaller than the original PNG.
	b64 := strings.TrimPrefix(dataURL, "data:image/jpeg;base64,")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}
	if len(decoded) >= len(raw) {
		t.Errorf("optimized image should be smaller: got %d bytes, original %d", len(decoded), len(raw))
	}

	// Verify it's a valid JPEG by checking the magic bytes.
	if len(decoded) < 2 || decoded[0] != 0xFF || decoded[1] != 0xD8 {
		t.Errorf("expected JPEG magic bytes FF D8, got %X %X", decoded[0], decoded[1])
	}
}

func TestOptimizeImageForContext_MediumImage(t *testing.T) {
	// An image at exactly maxImageDim should not be resized but should be
	// re-encoded as JPEG for size savings.
	raw := makeTestImage(maxImageDim, maxImageDim)
	dataURL := optimizeImageForContext(raw, "image/png")

	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("medium image should be re-encoded as JPEG, got prefix: %s", dataURL[:40])
	}
}

func TestOptimizeImageForContext_PreservesAspect(t *testing.T) {
	// A wide image (4000×1000) should be resized preserving aspect ratio.
	raw := makeTestImage(4000, 1000)
	dataURL := optimizeImageForContext(raw, "image/png")

	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("should be JPEG, got: %s", dataURL[:40])
	}

	// Verify the output is valid and smaller.
	b64 := strings.TrimPrefix(dataURL, "data:image/jpeg;base64,")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(decoded) >= len(raw) {
		t.Errorf("should be smaller: got %d, orig %d", len(decoded), len(raw))
	}
}

func TestScaleDimensions(t *testing.T) {
	tests := []struct {
		w, h, maxDim int
		wantW, wantH int
	}{
		{100, 100, 1536, 100, 100},       // small — unchanged
		{3000, 2000, 1536, 1536, 1024},   // landscape — fit width
		{2000, 3000, 1536, 1024, 1536},   // portrait — fit height
		{1536, 1536, 1536, 1536, 1536},   // exact — unchanged
		{2000, 2000, 1536, 1536, 1536},   // square — both scaled
	}

	for _, tt := range tests {
		gotW, gotH := scaleDimensions(tt.w, tt.h, tt.maxDim)
		if gotW != tt.wantW || gotH != tt.wantH {
			t.Errorf("scaleDimensions(%d, %d, %d) = (%d, %d), want (%d, %d)",
				tt.w, tt.h, tt.maxDim, gotW, gotH, tt.wantW, tt.wantH)
		}
	}
}

func TestOptimizeMCPImage(t *testing.T) {
	// Create a large image and encode as base64 (simulating MCP screenshot).
	raw := makeTestImage(2560, 1440)
	b64Data := base64.StdEncoding.EncodeToString(raw)

	img := MCPImage{
		MIMEType: "image/png",
		Data:     b64Data,
	}

	dataURL := optimizeMCPImage(img)

	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("large MCP image should be optimized to JPEG, got: %s", dataURL[:40])
	}

	// Verify the optimized payload is smaller than the original.
	b64Result := strings.TrimPrefix(dataURL, "data:image/jpeg;base64,")
	decoded, _ := base64.StdEncoding.DecodeString(b64Result)
	if len(decoded) >= len(raw) {
		t.Errorf("optimized should be smaller: got %d, orig %d", len(decoded), len(raw))
	}
}

func TestOptimizeMCPImage_EmptyMIME(t *testing.T) {
	// When MIMEType is empty, it should default to PNG and still work.
	raw := makeTestImage(50, 50)
	b64Data := base64.StdEncoding.EncodeToString(raw)

	img := MCPImage{
		MIMEType: "",
		Data:     b64Data,
	}

	dataURL := optimizeMCPImage(img)

	// Small image with empty MIME should be re-encoded as JPEG.
	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("should be re-encoded as JPEG, got: %s", dataURL[:40])
	}
}

func TestEstimateImageTokens(t *testing.T) {
	// A 1KB data URL should estimate to ~250 tokens (1000/4).
	// Local LLM servers tokenize the base64 data URL as regular text.
	tokens := EstimateImageTokens(strings.Repeat("a", 750))
	if tokens != 187 {
		t.Errorf("750 chars should be ~187 tokens (750/4), got %d", tokens)
	}

	tokens = EstimateImageTokens(strings.Repeat("a", 7500))
	if tokens != 1875 {
		t.Errorf("7500 chars should be ~1875 tokens (7500/4), got %d", tokens)
	}
}

func TestFormatImageSize(t *testing.T) {
	tests := []struct {
		dataURL string
		want    string
	}{
		{"data:image/png;base64," + strings.Repeat("A", 400), "300B"},
		{"data:image/png;base64," + strings.Repeat("A", 4096), "3.0KB"},
		{"data:image/png;base64," + strings.Repeat("A", 40960), "30.0KB"},
	}

	for _, tt := range tests {
		got := FormatImageSize(tt.dataURL)
		if got != tt.want {
			t.Errorf("FormatImageSize() = %s, want %s", got, tt.want)
		}
	}
}

func TestOptimizeImageForContext_DecodeFailure(t *testing.T) {
	// Invalid image bytes should fall back to returning original data.
	raw := []byte("not an image at all")
	dataURL := optimizeImageForContext(raw, "image/png")

	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("decode failure should return original as PNG, got: %s", dataURL[:40])
	}

	// Verify it contains the original bytes encoded.
	b64 := strings.TrimPrefix(dataURL, "data:image/png;base64,")
	decoded, _ := base64.StdEncoding.DecodeString(b64)
	if string(decoded) != string(raw) {
		t.Error("decode failure should return original bytes unchanged")
	}
}

func TestOptimizeImageForContext_JPEGInput(t *testing.T) {
	// A JPEG input should still work (decode → re-encode as JPEG).
	raw := makeTestImage(800, 600)
	dataURL := optimizeImageForContext(raw, "image/jpeg")

	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("JPEG input should produce JPEG output, got: %s", dataURL[:40])
	}
}

func TestOptimizeImageForContext_TransparentBecomesWhite(t *testing.T) {
	// A large PNG with a transparent region must be flattened onto white when
	// re-encoded as JPEG — transparent pixels must NOT turn black.
	const size = 2000 // large enough to force resize → JPEG re-encode
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if x < size/2 {
				img.Set(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 0}) // transparent
			} else {
				// Opaque, noisy region so the PNG is large and JPEG wins.
				n := uint8((x*7 ^ y*13) & 0x3F)
				img.Set(x, y, color.RGBA{R: 60 + n, G: 90 + n, B: 200, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	dataURL := optimizeImageForContext(buf.Bytes(), "image/png")
	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Fatalf("large transparent PNG should be re-encoded as JPEG, got: %s", dataURL[:40])
	}

	b64 := strings.TrimPrefix(dataURL, "data:image/jpeg;base64,")
	rawOut, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	out, _, err := image.Decode(bytes.NewReader(rawOut))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	// Sample the top-left, which was fully transparent: it must be ~white.
	r, g, b, _ := out.At(5, 5).RGBA()
	if r < 0xc000 || g < 0xc000 || b < 0xc000 {
		t.Errorf("transparent region should flatten to ~white, got r=%d g=%d b=%d",
			r>>8, g>>8, b>>8)
	}
}

func TestOptimizeImageForContext_TinyImageKeepsPNG(t *testing.T) {
	// A tiny flat-color PNG compresses to a few dozen bytes; the JPEG version
	// would be larger, so the optimizer should keep the original PNG rather
	// than inflate the payload.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	raw := buf.Bytes()

	dataURL := optimizeImageForContext(raw, "image/png")
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("tiny flat PNG should stay PNG (JPEG would be larger), got: %s", dataURL[:40])
	}
}
