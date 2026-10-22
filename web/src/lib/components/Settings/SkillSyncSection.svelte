<script>
	import { apiPost } from '$lib/api.js';

	let syncing = false;
	let result = '';

	async function sync() {
		syncing = true;
		result = '';
		const res = await apiPost('/api/skills/sync-all');
		if (res?.success) {
			const d = res.data;
			result = `Synced ${d.synced} skills to ${d.projects} projects` + (d.errors > 0 ? ` (${d.errors} errors)` : '');
		} else {
			result = 'Error: ' + (res?.message || 'Sync failed');
		}
		syncing = false;
		setTimeout(() => result = '', 5000);
	}
</script>

<div class="card">
	<h2 class="section-title">Skill Sync</h2>
	<p class="sync-desc">Sync all enabled skills to each project's <code>.claude/skills/</code> directory so Claude Code and OpenCode can use them natively with frontmatter (effort, maxTurns, etc).</p>
	<div class="sync-actions">
		<button class="btn btn-sm" onclick={sync} disabled={syncing}>
			{syncing ? 'Syncing...' : 'Sync All Skills'}
		</button>
		{#if result}
			<span class="sync-msg" class:error={result.startsWith('Error')}>{result}</span>
		{/if}
	</div>
</div>

<style>
	.card {
		max-width: none;
	}
	.section-title {
		font-family: var(--font-display);
		font-size: 26px;
		font-weight: 400;
		letter-spacing: -0.01em;
		margin-bottom: 12px;
	}
	.sync-desc {
		font-size: 13px;
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
		max-width: 70ch;
	}
	.sync-desc code {
		background: var(--bg-tertiary, #2a2a2a);
		padding: 1px 5px;
		border-radius: 3px;
		font-size: 12px;
	}
	.sync-actions {
		display: flex;
		align-items: center;
		gap: 12px;
	}
	.sync-msg {
		font-size: 13px;
		color: var(--success);
	}
	.sync-msg.error {
		color: var(--danger);
	}
</style>
