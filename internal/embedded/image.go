package embedded

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	"golang.org/x/image/draw"
)

// maxImageDim is the maximum width or height an image may have after resizing.
// 1536 is chosen because: (a) it's well within what modern vision models
// (Qwen2-VL, LLaVA-NeXT) accept, (b) it preserves enough detail for UI
// screenshots and photo analysis, and (c) it keeps base64 payload under ~1 MB
// for typical screenshots after JPEG re-encoding at quality 85.
const maxImageDim = 1536

// jpegQuality is the quality level used when re-encoding PNG screenshots as
// JPEG to reduce payload. 85 is visually lossless for screenshots and photos
// while cutting size by 60–80 % vs PNG.
const jpegQuality = 85

// optimizeImageForContext takes raw image bytes (typically a PNG screenshot
// from mobile_take_screenshot or a file read by read_file) and returns a
// data URL suitable for the chat context. If the image dimensions exceed
// maxImageDim it is resized so the longest dimension does not exceed the cap,
// then re-encoded as JPEG at quality 85 — a process that preserves visual
// quality for UI screenshots and photos while cutting the base64 payload by
// up to 80 %.
//
// Images already within dimension limits are still re-encoded as JPEG (from
// PNG) since screenshots are typically 60–80 % smaller as JPEG with no visible
// quality loss. Small JPEGs are left as-is.
//
// Returns the original dataURL unchanged if decoding fails (we prefer a large
// image over no image).
func optimizeImageForContext(rawBytes []byte, origMIME string) string {
	mime := origMIME
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}

	// Decode the image. If decoding fails, return original unchanged — better
	// to send a large image than no image at all.
	img, _, err := image.Decode(bytes.NewReader(rawBytes))
	if err != nil {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(rawBytes)
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Resize if either dimension exceeds maxImageDim.
	if w > maxImageDim || h > maxImageDim {
		newW, newH := scaleDimensions(w, h, maxImageDim)
		resized := image.NewRGBA(image.Rect(0, 0, newW, newH))
		draw.CatmullRom.Scale(resized, resized.Rect, img, bounds, draw.Over, nil)
		img = resized
	}

	// Always try JPEG re-encoding — for screenshots this cuts payload by
	// 60–80 % with negligible quality loss at quality 85.
	dataURL, ok := reencodeAsJPEG(img)
	if ok {
		return dataURL
	}

	// JPEG encoding failed (rare) — fall back to PNG.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err == nil {
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	}

	// Last resort: return original.
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(rawBytes)
}

// scaleDimensions computes new dimensions that fit within maxDim while
// preserving aspect ratio.
func scaleDimensions(w, h, maxDim int) (int, int) {
	if w <= maxDim && h <= maxDim {
		return w, h
	}
	ratio := float64(maxDim) / float64(max(w, h))
	newW := int(float64(w) * ratio)
	newH := int(float64(h) * ratio)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	return newW, newH
}

// reencodeAsJPEG encodes an image as JPEG at quality 85 and returns a data URL.
// Returns false if encoding fails or the result is larger than the original
// (which can happen for tiny images where JPEG overhead dominates).
func reencodeAsJPEG(img image.Image) (string, bool) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
	if err != nil || buf.Len() == 0 {
		return "", false
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// optimizeMCPImage applies the same optimization to an MCPImage (which carries
// base64-encoded data without the data: prefix). It returns the optimized data
// URL ready for pendingImage.
func optimizeMCPImage(img MCPImage) string {
	rawBytes, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		// Can't decode base64 — use original as-is.
		mime := img.MIMEType
		if mime == "" {
			mime = "image/png"
		}
		return "data:" + mime + ";base64," + img.Data
	}
	mime := img.MIMEType
	if mime == "" {
		mime = "image/png"
	}
	return optimizeImageForContext(rawBytes, mime)
}

// optimizeImageFile reads an image from disk and returns an optimized data URL.
// This is used by read_file when it encounters an image file in vision mode.
func optimizeImageFile(data []byte, mime string) string {
	if !strings.HasPrefix(mime, "image/") {
		mime = http.DetectContentType(data)
	}
	return optimizeImageForContext(data, mime)
}

// EstimateImageTokens provides a rough token estimate for a base64 data URL,
// useful for logging and budget tracking. OpenAI's vision models use ~85 tokens
// per 512×512 tile; we approximate as bytes/750 since base64 in JSON is ~4/3×
// the binary size and each token is ~4 bytes of JSON text.
func EstimateImageTokens(dataURL string) int {
	return len(dataURL) / 750
}

// FormatImageSize returns a human-readable size string for logging.
func FormatImageSize(dataURL string) string {
	// Strip "data:...;base64," prefix to get just the base64 payload.
	idx := strings.Index(dataURL, ",")
	if idx < 0 {
		return "?"
	}
	b64Len := len(dataURL) - idx - 1
	rawBytes := b64Len * 3 / 4 // approximate decoded size
	switch {
	case rawBytes < 1024:
		return fmt.Sprintf("%dB", rawBytes)
	case rawBytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(rawBytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(rawBytes)/(1024*1024))
	}
}
