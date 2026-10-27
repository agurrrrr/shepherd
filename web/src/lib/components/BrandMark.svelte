<script>
	// Shepherd mark: a pixel crook whose hook carries a yellow lantern. The
	// lantern blinks (steps(1)) when `live` — the same awake signal the rest of
	// the UI uses. Maps live in $lib/pixel/sprites.js; 'x' is currentColor and
	// 'y' paints var(--px-y) (PICO yellow).
	import { CROOK } from '$lib/pixel/sprites.js';

	let { size = 28, live = false } = $props();

	const LANTERN = ['yy', 'yy', 'yy'];

	function rects(map, ox = 0, oy = 0) {
		let out = '';
		map.forEach((row, y) => {
			let x = 0;
			while (x < row.length) {
				const c = row[x];
				if (c === '.') { x++; continue; }
				let run = 1;
				while (row[x + run] === c) run++;
				const fill = c === 'x' ? 'currentColor' : `var(--px-${c})`;
				out += `<rect x="${x + ox}" y="${y + oy}" width="${run}" height="1" fill="${fill}"/>`;
				x += run;
			}
		});
		return out;
	}

	// Crook is 9×9 cells; the viewBox is in cell units so the whole mark scales
	// with `size`. The lantern hangs inside the hook, left of the shaft.
	const body = rects(CROOK);
	const lantern = rects(LANTERN, 4, 5);
</script>

<span class="mark" class:live style="--size: {size}px" aria-hidden="true">
	<svg class="px" viewBox="0 0 9 9" width={size} height={size} shape-rendering="crispEdges">
		{@html body}
		<g class="lantern">{@html lantern}</g>
	</svg>
</span>

<style>
	.mark {
		display: inline-flex;
		width: var(--size);
		height: var(--size);
		color: var(--text-primary);
		flex-shrink: 0;
	}

	.px {
		display: block;
		image-rendering: pixelated;
	}

	.live .lantern {
		animation: px-blink 1s steps(1) infinite;
	}
</style>
