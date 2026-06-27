package embedded

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // register the PNG decoder for image.Decode
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
// Images already within dimension limits are still re-encoded as JPEG since
// screenshots are typically 60–80 % smaller as JPEG with no visible quality
// loss. But if the JPEG ends up larger than the original (common for tiny
// icons or already-optimized PNGs), the original is kept instead — we never
// want optimization to inflate the payload.
//
// Returns the original dataURL unchanged if decoding fails (we prefer a large
// image over no image).
func optimizeImageForContext(rawBytes []byte, origMIME string) string {
	mime := origMIME
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}
	origDataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(rawBytes)

	// Decode the image. If decoding fails, return original unchanged — better
	// to send a large image than no image at all.
	img, _, err := image.Decode(bytes.NewReader(rawBytes))
	if err != nil {
		return origDataURL
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Resize if either dimension exceeds maxImageDim.
	resized := false
	if w > maxImageDim || h > maxImageDim {
		newW, newH := scaleDimensions(w, h, maxImageDim)
		dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
		draw.CatmullRom.Scale(dst, dst.Rect, img, bounds, draw.Over, nil)
		img = dst
		resized = true
	}

	// JPEG has no alpha channel: encoding an image with transparency directly
	// renders transparent regions as black. Flatten onto white (the typical
	// screenshot/UI background) so transparent PNGs survive re-encoding intact.
	img = flattenOntoWhite(img)

	jpegBytes, err := encodeJPEG(img)
	if err != nil || len(jpegBytes) == 0 {
		// JPEG encoding failed (rare) — keep the original bytes.
		return origDataURL
	}

	// Use the JPEG when we actually shrank the dimensions (smaller bytes are the
	// whole point) or when the JPEG is genuinely smaller than the original. For
	// tiny, already-well-compressed PNGs the JPEG can be larger — keep PNG then.
	if resized || len(jpegBytes) < len(rawBytes) {
		return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes)
	}
	return origDataURL
}

// flattenOntoWhite composites img over an opaque white background, removing any
// alpha channel. Fully opaque images are unchanged visually; transparent
// regions become white instead of the black that JPEG would otherwise produce.
func flattenOntoWhite(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
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

// encodeJPEG encodes an image as JPEG at quality 85 and returns the raw bytes.
// The caller decides whether the result is worth using over the original.
func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
