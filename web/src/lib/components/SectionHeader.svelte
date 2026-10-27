<script>
	// Section heading inside a page. `index` prints an ordinal ("01") before
	// the eyebrow; `tone` tints the eyebrow (live).
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

	/* "01 —— PASTURE": yellow ordinal, pink 8px eyebrow (blue when live) */
	.index {
		font-family: var(--font-label);
		font-size: var(--fs-label);
		line-height: 2;
		color: var(--highlight);
		letter-spacing: 0;
	}

	.index::after {
		content: '';
		display: inline-block;
		width: 12px;
		height: 3px;
		margin-left: 10px;
		vertical-align: middle;
		background: var(--hi);
	}

	.eyebrow-row .eyebrow {
		color: var(--accent);
	}

	.section-header[data-tone="live"] .eyebrow {
		color: var(--live);
	}

	.title {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
		line-height: 1.5;
		letter-spacing: 0;
		color: var(--text-primary);
		text-shadow: 3px 3px 0 #000;
		margin: 0;
	}

	.subtitle {
		font-size: var(--fs-xs);
		font-family: var(--font-mono);
		color: var(--text-tertiary);
		margin: 0;
		letter-spacing: 0;
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
			font-size: var(--fs-lg);
		}

		/* Narrow actions (e.g. usage filter + segmented tabs) must fit the
		   viewport and wrap onto their own line instead of pushing the page
		   sideways (issue #636 §5-6). */
		.actions {
			flex-shrink: 1;
			min-width: 0;
			max-width: 100%;
		}
	}
</style>
