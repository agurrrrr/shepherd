<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiDelete, apiPatch } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';

	let sheepList = [];
	let loaded = false;
	let showAdd = false;
	let newName = '';
	let newProvider = 'claude';
	let adding = false;
	let unsubs = [];

	onMount(async () => {
		await loadSheep();

		unsubs.push(onSSE('status_change', (data) => {
			sheepList = sheepList.map(s =>
				s.name === data.sheep_name ? { ...s, status: data.status } : s
			);
		}));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	async function loadSheep() {
		const res = await apiGet('/api/sheep');
		if (res?.data) sheepList = res.data;
		loaded = true;
	}

	async function addSheep() {
		adding = true;
		const body = { provider: newProvider };
		if (newName) body.name = newName;
		const res = await apiPost('/api/sheep', body);
		if (res?.success) {
			newName = '';
			showAdd = false;
			await loadSheep();
		}
		adding = false;
	}

	async function removeSheep(name) {
		if (!confirm(`Remove sheep "${name}"?`)) return;
		await apiDelete(`/api/sheep/${encodeURIComponent(name)}`);
		await loadSheep();
	}

	async function changeProvider(name, provider) {
		await apiPatch(`/api/sheep/${encodeURIComponent(name)}/provider`, { provider });
		await loadSheep();
	}
</script>

<div class="page">
	<div class="page-header">
		<h1>Sheep</h1>
		<button class="btn btn-primary" onclick={() => showAdd = !showAdd}>
			{showAdd ? 'Cancel' : '+ Spawn Sheep'}
		</button>
	</div>

	{#if showAdd}
		<div class="card add-form">
			<div class="form-row">
				<div class="form-group">
					<label>Name (optional)</label>
					<input class="input" bind:value={newName} placeholder="Auto-assign" />
				</div>
				<div class="form-group">
					<label>Provider</label>
					<select class="input" bind:value={newProvider}>
						<option value="claude">Claude</option>
						<option value="opencode">OpenCode</option>
						<option value="auto">Auto</option>
					</select>
				</div>
			</div>
			<button class="btn btn-primary" onclick={addSheep} disabled={adding}>
				{adding ? 'Spawning...' : 'Spawn'}
			</button>
		</div>
	{/if}

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if sheepList.length === 0}
		<div class="card empty-state">
			<p>No sheep yet.</p>
			<p class="text-muted">Spawn a sheep to start processing tasks.</p>
		</div>
	{:else}
		<div class="sheep-grid">
			{#each sheepList as s}
				<div class="card sheep-card">
					<div class="sheep-header">
						<span class="sheep-name">{s.name}</span>
						<span class="badge badge-{s.status}">{s.status}</span>
					</div>
					{#if s.project}
						<div class="sheep-project">{s.project}</div>
					{/if}
					<div class="sheep-meta">
						<select class="input provider-select" value={s.provider}
							onchange={(e) => changeProvider(s.name, e.target.value)}>
							<option value="claude">Claude</option>
							<option value="opencode">OpenCode</option>
							<option value="auto">Auto</option>
						</select>
						<button class="btn btn-danger btn-sm" onclick={() => removeSheep(s.name)}>Remove</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.page-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 20px;
	}

	.page-header h1 { font-size: 24px; font-weight: 600; }
	.text-muted { color: var(--text-secondary); }

	.add-form {
		margin-bottom: 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.form-row {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 12px;
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.form-group label {
		font-size: 12px;
		color: var(--text-secondary);
		font-weight: 500;
	}

	.form-group .input { width: 100%; }

	.sheep-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
		gap: 12px;
	}

	.sheep-card {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.sheep-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.sheep-name { font-weight: 600; font-size: 15px; }
	.sheep-project { font-size: 13px; color: var(--accent); }

	.sheep-meta {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-top: 4px;
	}

	.provider-select {
		flex: 1;
		padding: 4px 8px;
		font-size: 12px;
	}

	.btn-sm { padding: 4px 10px; font-size: 12px; }

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	@media (max-width: 768px) {
		.sheep-grid {
			grid-template-columns: 1fr;
		}

		.form-row {
			grid-template-columns: 1fr;
		}

		.provider-select {
			padding: 6px 10px;
			font-size: 13px;
		}

		.btn-sm {
			padding: 6px 12px;
		}

		.page-header h1 {
			font-size: 20px;
		}
	}
</style>
