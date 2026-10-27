<script>
	// Pixel icons: each glyph is a map of '.'/'x' strings (see $lib/pixel/icons.js)
	// drawn as one SVG <rect> per run with crisp edges. Sprite letters (o/w/k/f…)
	// paint the --px-* sprite tokens so the sheep keeps its wool and face.
	// Icons without a map fall back to the old Lucide strokes, but with square
	// caps and miter joins so they still read as pixel art.
	import { PIXEL_ICONS } from '$lib/pixel/icons.js';

	let { name = 'circle', size = 16, stroke = 2, label = '', class: klass = '' } = $props();

	// Coalesce horizontal runs into rects once per icon (module-level memo).
	const rectCache = new Map();

	function rects(map) {
		if (rectCache.has(map)) return rectCache.get(map);
		const w = Math.max(...map.map((r) => r.length));
		let out = '';
		map.forEach((row, y) => {
			let x = 0;
			while (x < row.length) {
				const c = row[x];
				if (c === '.') { x++; continue; }
				let run = 1;
				while (row[x + run] === c) run++;
				const fill = c === 'x' ? 'currentColor' : `var(--px-${c})`;
				out += `<rect x="${x}" y="${y}" width="${run}" height="1" fill="${fill}"/>`;
				x += run;
			}
		});
		const dims = { w, h: map.length, body: out };
		rectCache.set(map, dims);
		return dims;
	}

	const LUCIDE = {
		// Only icons with no pixel map live here. Keep stroke caps square.
	};

	let pix = $derived(PIXEL_ICONS[name]);
	let lucide = $derived(!pix && LUCIDE[name]);
</script>

{#if pix}
	{@const d = rects(pix)}
	<svg
		class="icon px {klass}"
		width={size}
		height={size}
		viewBox="0 0 {d.w} {d.h}"
		shape-rendering="crispEdges"
		role={label ? 'img' : 'presentation'}
		aria-label={label || undefined}
		aria-hidden={label ? undefined : 'true'}
	>{@html d.body}</svg>
{:else if lucide}
	<svg
		class="icon {klass}"
		width={size}
		height={size}
		viewBox="0 0 24 24"
		fill="none"
		stroke="currentColor"
		stroke-width={stroke}
		stroke-linecap="square"
		stroke-linejoin="miter"
		role={label ? 'img' : 'presentation'}
		aria-label={label || undefined}
		aria-hidden={label ? undefined : 'true'}
	>{@html lucide}</svg>
{/if}

<style>
	.icon {
		display: inline-block;
		flex-shrink: 0;
		vertical-align: middle;
	}

	.icon.px {
		image-rendering: pixelated;
	}
</style>
