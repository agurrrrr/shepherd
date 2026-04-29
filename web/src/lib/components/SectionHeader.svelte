<script>
	let { title = '', eyebrow = '', subtitle = '', tone = 'default', children } = $props();
</script>

<header class="section-header" data-tone={tone}>
	<div class="text">
		{#if eyebrow}
			<span class="eyebrow">{eyebrow}</span>
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
		gap: var(--space-3);
		margin: 0 0 var(--space-3);
	}

	.text {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.eyebrow {
		font-size: var(--fs-2xs);
		font-weight: var(--fw-semibold);
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.section-header[data-tone="live"] .eyebrow {
		color: var(--live);
	}

	.title {
		font-size: var(--fs-lg);
		font-weight: var(--fw-semibold);
		line-height: var(--lh-tight);
		letter-spacing: -0.01em;
		color: var(--text-primary);
		margin: 0;
	}

	.subtitle {
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		margin: 0;
	}

	.actions {
		flex-shrink: 0;
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}

	@media (max-width: 480px) {
		.section-header {
			margin: 0 0 var(--space-2);
		}

		.title {
			font-size: var(--fs-md);
		}
	}
</style>
