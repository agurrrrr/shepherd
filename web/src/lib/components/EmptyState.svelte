<script>
	let { title = '', description = '', icon = 'sheep', children } = $props();
</script>

<div class="empty">
	<div class="empty-icon" aria-hidden="true">
		{#if icon === 'sheep'}
			<svg width="56" height="56" viewBox="0 0 64 64" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
				<!-- Wool body -->
				<circle cx="20" cy="32" r="6" />
				<circle cx="32" cy="26" r="7" />
				<circle cx="44" cy="32" r="6" />
				<circle cx="26" cy="38" r="6" />
				<circle cx="38" cy="38" r="6" />
				<!-- Head -->
				<ellipse cx="50" cy="34" rx="6" ry="5" />
				<!-- Ear -->
				<path d="M53 30 L57 27" />
				<!-- Eye -->
				<circle cx="52" cy="33" r="0.6" fill="currentColor" stroke="none" />
				<!-- Legs -->
				<line x1="22" y1="44" x2="22" y2="50" />
				<line x1="30" y1="46" x2="30" y2="52" />
				<line x1="40" y1="46" x2="40" y2="52" />
				<line x1="48" y1="44" x2="48" y2="50" />
			</svg>
		{:else if icon === 'list'}
			<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
				<line x1="8" y1="6" x2="21" y2="6"/>
				<line x1="8" y1="12" x2="21" y2="12"/>
				<line x1="8" y1="18" x2="21" y2="18"/>
				<line x1="3" y1="6" x2="3.01" y2="6"/>
				<line x1="3" y1="12" x2="3.01" y2="12"/>
				<line x1="3" y1="18" x2="3.01" y2="18"/>
			</svg>
		{:else if icon === 'inbox'}
			<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
				<polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/>
				<path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11Z"/>
			</svg>
		{/if}
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
		padding: var(--space-7) var(--space-4);
		gap: var(--space-2);
		color: var(--text-secondary);
	}

	.empty-icon {
		color: var(--wool);
		margin-bottom: var(--space-2);
		opacity: 0.7;
	}

	.empty-title {
		font-size: var(--fs-md);
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
		margin: 0;
	}

	.empty-desc {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		max-width: 360px;
		margin: 0;
		line-height: var(--lh-snug);
	}

	.empty-actions {
		margin-top: var(--space-3);
	}

	@media (max-width: 480px) {
		.empty {
			padding: var(--space-5) var(--space-3);
		}
	}
</style>
