// Image compression for task-chat attachments (CommandInput.svelte).
//
// Uploads go to POST /api/upload, which stores files under the project's
// .shepherd-uploads directory and rejects anything over 10 MB. A phone photo
// or a full-desktop screenshot can easily be 5-15 MB, so the browser
// compresses before upload: re-encode as JPEG at quality 0.90 and cap the
// longest side at MAX_DIM pixels. The result is only used when it is smaller
// than the original — an already-optimized JPEG or a tiny icon keeps its
// original bytes, so compression never inflates a payload.
//
// Server side keeps its own 10 MB guard in internal/server/handlers_upload.go;
// this module runs before that check, so a big PNG screenshot that compresses
// below 10 MB uploads fine.

/** JPEG re-encode quality: 90 %, visually lossless for screenshots and photos. */
export const JPEG_QUALITY = 0.9;

/** Longest side in pixels after compression. Larger images are downscaled. */
export const MAX_DIM = 4096;

/** Server limit in internal/server/handlers_upload.go — stay at or under it. */
export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;

/** How many extra downscale rounds to try when q90 still exceeds the limit. */
const MAX_SIZE_ROUNDS = 4;

/** Each round multiplies the current side by this factor. */
const SHRINK_STEP = 0.75;

/**
 * Whether a MIME type is safe to re-encode. GIF is excluded because canvas
 * would flatten an animation to its first frame; non-images pass through.
 * @param {string} mime
 */
export function isCompressibleImage(mime) {
	return typeof mime === 'string' && mime.startsWith('image/') && mime !== 'image/gif';
}

/**
 * Dimensions that fit within maxDim while preserving the aspect ratio.
 * @param {number} w
 * @param {number} h
 * @param {number} [maxDim]
 * @returns {{ w: number, h: number }}
 */
export function scaleDimensions(w, h, maxDim = MAX_DIM) {
	if (w <= maxDim && h <= maxDim) return { w, h };
	const ratio = maxDim / Math.max(w, h);
	return {
		w: Math.max(1, Math.round(w * ratio)),
		h: Math.max(1, Math.round(h * ratio))
	};
}

/**
 * Compress one image file for upload. Non-images, GIFs, decode failures and
 * encode failures return the original file unchanged — a large image is
 * better than no image.
 *
 * Quality is fixed at JPEG_QUALITY; size is cut by downscaling instead, so
 * the "90 %" promise never degrades. If the q90 encode is still over
 * maxBytes (rare past MAX_DIM), the longest side shrinks step by step and
 * re-encodes, up to MAX_SIZE_ROUNDS times. The result is only used when it
 * is smaller than the original.
 * @param {File} file
 * @param {number} [maxBytes]
 * @returns {Promise<File>} the JPEG File when it is smaller, otherwise the original
 */
export async function compressImageFile(file, maxBytes = MAX_UPLOAD_BYTES) {
	if (!isCompressibleImage(file.type)) return file;

	let bitmap;
	try {
		bitmap = await createImageBitmap(file);
	} catch {
		return file; // browser cannot decode it (HEIC, SVG, corrupt) — upload as-is
	}

	try {
		let { w, h } = scaleDimensions(bitmap.width, bitmap.height);
		let blob = null;
		for (let round = 0; round <= MAX_SIZE_ROUNDS; round++) {
			blob = await encodeCanvasJpeg(bitmap, w, h);
			if (!blob) return file; // canvas or toBlob failed — upload as-is
			if (blob.size <= maxBytes) break;
			w = Math.max(1, Math.round(w * SHRINK_STEP));
			h = Math.max(1, Math.round(h * SHRINK_STEP));
		}
		if (!blob || blob.size >= file.size) return file;

		const baseName = file.name.replace(/\.[^.]*$/, '');
		return new File([blob], `${baseName}.jpg`, { type: 'image/jpeg' });
	} catch {
		return file;
	} finally {
		bitmap.close?.();
	}
}

/**
 * Draw the bitmap into a w×h canvas (flattened onto white, since JPEG has no
 * alpha channel) and encode it as JPEG at JPEG_QUALITY.
 * @param {ImageBitmap} bitmap
 * @param {number} w
 * @param {number} h
 * @returns {Promise<Blob | null>}
 */
async function encodeCanvasJpeg(bitmap, w, h) {
	const canvas = document.createElement('canvas');
	canvas.width = w;
	canvas.height = h;
	const ctx = canvas.getContext('2d');
	if (!ctx) return null;

	ctx.fillStyle = '#ffffff';
	ctx.fillRect(0, 0, w, h);
	ctx.imageSmoothingEnabled = true;
	ctx.imageSmoothingQuality = 'high';
	ctx.drawImage(bitmap, 0, 0, w, h);

	return new Promise((resolve) => {
		canvas.toBlob((blob) => resolve(blob), 'image/jpeg', JPEG_QUALITY);
	});
}
