<script>
	// Section heading inside a page. `index` prints an editorial ordinal
	// ("01") before the eyebrow; `tone` tints the eyebrow (live, accent).
	let { title = '', eyebrow = '', subtitle = '', tone = 'default', index = '', children } = $props();
</script>

<header class="section-header" data-tone={tone}>
	<div class="text">
		{#if eyebrow || index}
			<span class="eyebrow-row">
				{#if index}<span class="index">{index}</span>{/if}
				{#if eyebrow}<span class="eyebrow">{eyebrow}</span>{/if}
			</span>
		{/if}
		<h2 class="title">{title}</h2>
		{#if subtitle}
			<p class="subtitle">{subtitle}</p>
		{/if}
	</div>
	{#if children}
		<div class="actions">
			{@render children()}
		</div>
	{/if}
</header>

<style>
	.section-header {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-3) var(--space-4);
		flex-wrap: wrap;
		margin: 0 0 var(--space-4);
	}

	.text {
		display: flex;
		flex-direction: column;
		gap: 6px;
		min-width: 0;
	}

	.eyebrow-row {
		display: inline-flex;
		align-items: center;
		gap: 10px;
	}

	.index {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--accent);
		letter-spacing: 0.04em;
	}

	.index::after {
		content: '';
		display: inline-block;
		width: 18px;
		height: 1px;
		margin-left: 10px;
		vertical-align: middle;
		background: var(--accent-line);
	}

	.section-header[data-tone="live"] .eyebrow,
	.section-header[data-tone="live"] .index {
		color: var(--live);
	}

	.section-header[data-tone="live"] .index::after {
		background: var(--live-line);
	}

	.title {
		font-family: var(--font-display);
		font-size: 30px;
		font-weight: 400;
		line-height: 1;
		letter-spacing: -0.015em;
		color: var(--text-primary);
		margin: 0;
	}

	.subtitle {
		font-size: var(--fs-xs);
		font-family: var(--font-mono);
		color: var(--text-tertiary);
		margin: 0;
		letter-spacing: -0.01em;
	}

	.actions {
		flex-shrink: 0;
		display: flex;
		gap: var(--space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	@media (max-width: 480px) {
		.section-header {
			margin: 0 0 var(--space-3);
		}

		.title {
			font-size: 26px;
		}
	}
</style>
