<script>
	// Shared rendering for a parsed unified diff (parseUnifiedDiff response).
	// Used by the Git tab commit detail and the task changes card.
	let { diff = null, loading = false, maxHeight = '400px' } = $props();
</script>

<div class="diff-container" style:max-height={maxHeight}>
	{#if loading}
		<p class="text-muted diff-loading">Loading diff...</p>
	{:else if diff}
		{#if diff.is_binary}
			<p class="text-muted diff-binary">Binary file</p>
		{:else if !diff.hunks?.length}
			<p class="text-muted diff-empty">No diff available</p>
		{:else}
			{#each diff.hunks as hunk}
				<div class="diff-hunk">
					<div class="diff-hunk-header mono">{hunk.header}</div>
					{#each hunk.lines as line}
						<div class="diff-line diff-{line.type}">
							<span class="diff-ln old mono">{line.old_line || ''}</span>
							<span class="diff-ln new mono">{line.new_line || ''}</span>
							<span class="diff-prefix mono">{line.type === 'add' ? '+' : line.type === 'delete' ? '-' : ' '}</span>
							<span class="diff-code">{line.content}</span>
						</div>
					{/each}
				</div>
			{/each}
			{#if diff.truncated}
				<p class="diff-truncated">Diff truncated because it is too large.</p>
			{/if}
		{/if}
	{/if}
</div>

<style>
	.diff-container {
		margin: 4px 0 8px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
		max-height: 400px;
		overflow-y: auto;
	}

	.diff-loading, .diff-binary, .diff-empty {
		padding: 8px 12px;
		font-size: 12px;
	}

	.diff-truncated {
		margin: 0;
		padding: 6px 12px;
		font-size: 11px;
		color: var(--warning);
		background: var(--warning-soft);
		border-top: 1px solid var(--border);
	}

	.diff-hunk {
		border-bottom: 1px solid var(--border);
	}

	.diff-hunk:last-child {
		border-bottom: none;
	}

	.diff-hunk-header {
		font-size: 11px;
		color: var(--text-secondary);
		background: var(--bg-tertiary);
		padding: 4px 12px;
		border-bottom: 1px solid var(--border);
	}

	.diff-line {
		display: flex;
		font-family: var(--font-mono), 'SF Mono', Monaco, 'Cascadia Code', monospace;
		font-size: 12px;
		line-height: 20px;
		white-space: pre;
	}

	.diff-line:hover {
		filter: brightness(1.1);
	}

	.diff-line.diff-add {
		background: var(--success-soft);
	}

	.diff-line.diff-delete {
		background: var(--danger-soft);
	}

	.diff-line.diff-context {
		background: transparent;
	}

	.diff-ln {
		width: 40px;
		min-width: 40px;
		text-align: right;
		padding-right: 8px;
		color: var(--text-secondary);
		opacity: 0.5;
		user-select: none;
		flex-shrink: 0;
	}

	.diff-prefix {
		width: 20px;
		min-width: 20px;
		text-align: center;
		flex-shrink: 0;
		user-select: none;
	}

	.diff-add .diff-prefix {
		color: var(--success);
	}

	.diff-delete .diff-prefix {
		color: var(--danger);
	}

	.diff-code {
		flex: 1;
		min-width: 0;
		overflow-x: auto;
		padding-right: 12px;
	}
</style>
