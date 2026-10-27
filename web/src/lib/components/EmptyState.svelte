<script>
	// Empty state: a resting pixel sheep on a black well. The list/inbox kinds
	// are pixel maps from $lib/pixel/icons.js drawn through the same renderer.
	import { SHEEP_REST } from '$lib/pixel/sprites.js';

	let { title = '', description = '', icon = 'sheep', children } = $props();

	const LIST = [
		'xx.xxxxxx',
		'.........',
		'xx.xxxxxx',
		'.........',
		'xx.xxxxxx'
	];
	const INBOX = [
		'.xxxxxxx.',
		'xx.....xx',
		'xx.....xx',
		'xxx...xxx',
		'.xxxxxxx.',
		'..xx.xx..',
		'x.......x',
		'xxxxxxxxx'
	];

	function rects(map) {
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
		return { w, h: map.length, body: out };
	}

	const sprite = $derived(icon === 'list' ? rects(LIST) : icon === 'inbox' ? rects(INBOX) : rects(SHEEP_REST));
	const scale = 4;
</script>

<div class="empty">
	<div class="empty-icon" aria-hidden="true">
		<svg
			class="px"
			viewBox="0 0 {sprite.w} {sprite.h}"
			width={sprite.w * scale}
			height={sprite.h * scale}
			shape-rendering="crispEdges"
		>{@html sprite.body}</svg>
	</div>
	<h3 class="empty-title">{title}</h3>
	{#if description}
		<p class="empty-desc">{description}</p>
	{/if}
	{#if children}
		<div class="empty-actions">{@render children()}</div>
	{/if}
</div>

<style>
	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		text-align: center;
		padding: var(--space-8) var(--space-4);
		gap: var(--space-2);
		color: var(--text-secondary);
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.empty-icon {
		position: relative;
		display: grid;
		place-items: center;
		width: 96px;
		height: 96px;
		margin-bottom: var(--space-3);
		background: #000;
		box-shadow: inset 0 0 0 2px var(--hi);
		color: var(--wool);
	}

	.empty-icon :global(svg) {
		opacity: 0.92;
	}

	.empty-icon :global(.px) {
		image-rendering: pixelated;
	}

	.empty-title {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
		line-height: 1.5;
		letter-spacing: 0;
		color: var(--text-primary);
		text-shadow: 3px 3px 0 #000;
		margin: 0;
	}

	.empty-desc {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		max-width: 380px;
		margin: 0;
		line-height: var(--lh-snug);
	}

	.empty-actions {
		margin-top: var(--space-3);
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
		justify-content: center;
	}

	@media (max-width: 480px) {
		.empty {
			padding: var(--space-6) var(--space-3);
		}
	}
</style>
