<script>
	// Shepherd mark: a crook whose hook carries a lantern. The lantern glows
	// (and breathes when `live`) — the same signal the rest of the UI uses
	// for sheep that are awake and working.
	let { size = 28, live = false } = $props();

	// Unique gradient id per instance: a shared id breaks when the first
	// instance sits inside a display:none subtree (mobile top bar on desktop).
	const gid = `mark-glow-${Math.random().toString(36).slice(2, 8)}`;
</script>

<span class="mark" class:live style="--size: {size}px" aria-hidden="true">
	<svg viewBox="0 0 32 32" width={size} height={size} fill="none">
		<defs>
			<radialGradient id={gid} cx="50%" cy="50%" r="50%">
				<stop offset="0%" stop-color="#ffd38f" stop-opacity="0.9" />
				<stop offset="45%" stop-color="#f4b860" stop-opacity="0.35" />
				<stop offset="100%" stop-color="#f4b860" stop-opacity="0" />
			</radialGradient>
		</defs>
		<circle class="halo" cx="21.5" cy="16.5" r="8" fill="url(#{gid})" />
		<path d="M10.5 28V11.5a5.5 5.5 0 0 1 11 0v1.2" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
		<line x1="21.5" y1="12.7" x2="21.5" y2="14.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
		<rect class="lantern" x="19.6" y="14.3" width="3.8" height="4.6" rx="1.2" fill="#f4b860" />
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

	.lantern {
		filter: drop-shadow(0 0 3px rgba(244, 184, 96, 0.9));
	}

	.halo {
		transform-origin: 21.5px 16.5px;
		opacity: 0.75;
	}

	.live .halo {
		animation: breathe 3.2s ease-in-out infinite;
	}

	@keyframes breathe {
		0%, 100% { opacity: 0.55; transform: scale(0.9); }
		50% { opacity: 1; transform: scale(1.12); }
	}
</style>
