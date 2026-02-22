<script>
	import StatusBadge from './StatusBadge.svelte';
	import OutputViewer from './OutputViewer.svelte';
	import { onMount, onDestroy } from 'svelte';
	import { onSSE } from '$lib/sse.js';

	export let name = '';
	export let project = '';
	export let status = 'idle';
	export let provider = 'claude';

	let output = [];
	let unsubscribers = [];

	onMount(() => {
		unsubscribers.push(onSSE('output', (data) => {
			if (data.sheep_name === name || data.project_name === project) {
				output = [...output.slice(-100), data.text];
			}
		}));

		unsubscribers.push(onSSE('status_change', (data) => {
			if (data.sheep_name === name) {
				status = data.status;
			}
		}));
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn());
	});
</script>

<div class="sheep-card card" class:working={status === 'working'}>
	<div class="card-header">
		<div class="card-title">
			<span class="project-name">{project || name}</span>
			{#if project}
				<span class="sheep-name">{name}</span>
			{/if}
		</div>
		<StatusBadge {status} />
	</div>
	<div class="card-meta">
		<span class="provider">{provider}</span>
	</div>
	{#if output.length > 0}
		<OutputViewer lines={output} maxLines={50} />
	{/if}
</div>

<style>
	.sheep-card {
		transition: border-color 0.3s;
	}
	.sheep-card.working {
		border-color: var(--accent);
	}
	.card-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		margin-bottom: 8px;
	}
	.card-title {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.project-name {
		font-weight: 600;
		font-size: 14px;
	}
	.sheep-name {
		font-size: 12px;
		color: var(--text-secondary);
	}
	.card-meta {
		margin-bottom: 8px;
	}
	.provider {
		font-size: 12px;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}
</style>
