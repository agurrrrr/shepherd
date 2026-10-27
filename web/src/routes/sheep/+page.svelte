<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiDelete, apiPatch } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';

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
	<PageHeader
		eyebrow="Watch · Flock"
		title="Sheep"
		count={sheepList.length}
		subtitle="Each sheep is one agent bound to one project. Switch its provider here, or spawn a new one."
	>
		<button class="btn" class:btn-primary={!showAdd} onclick={() => showAdd = !showAdd} aria-expanded={showAdd}>
			<Icon name={showAdd ? 'x' : 'plus'} size={14} stroke={2.2} />
			{showAdd ? 'Cancel' : 'Spawn sheep'}
		</button>
	</PageHeader>

	{#if showAdd}
		<form class="card add-form" onsubmit={(e) => { e.preventDefault(); addSheep(); }}>
			<div class="form-row">
				<div class="form-group">
					<label for="ns-name">Name (optional)</label>
					<input id="ns-name" class="input" bind:value={newName} placeholder="Auto-assign" />
				</div>
				<div class="form-group">
					<label for="ns-provider">Provider</label>
					<select id="ns-provider" class="input" bind:value={newProvider}>
						<option value="claude">Claude</option>
						<option value="opencode">OpenCode</option>
						<option value="pi">Pi</option>
						<option value="grok">Grok</option>
						<option value="embedded">Embedded</option>
						<option value="magi">MAGI 🧠</option>
						<option value="auto">Auto</option>
					</select>
				</div>
				<button type="submit" class="btn btn-primary" disabled={adding}>
					{adding ? 'Spawning…' : 'Spawn'}
				</button>
			</div>
		</form>
	{/if}

	{#if !loaded}
		<div class="sheep-grid" aria-busy="true">
			{#each Array(12) as _}
				<div class="card sheep-card"><div class="skeleton" style="height: 28px; width: 28px"></div><div class="skeleton" style="height: 10px; width: 60%; margin-top: 12px"></div></div>
			{/each}
		</div>
	{:else if sheepList.length === 0}
		<div class="card">
			<EmptyState icon="sheep" title="An empty pasture" description="Spawn a sheep to start processing tasks." />
		</div>
	{:else}
		<div class="sheep-grid">
			{#each sheepList as s, i (s.name)}
				<div class="card sheep-card reveal" class:working={s.status === 'working'} class:error={s.status === 'error'} style="--i: {i}">
					<div class="sheep-header">
						<span class="ava" aria-hidden="true">{[...s.name][0]}</span>
						<div class="who">
							<span class="sheep-name">{s.name}</span>
							{#if s.project}
								<a class="sheep-project" href="/projects/{encodeURIComponent(s.project)}">{s.project}</a>
							{:else}
								<span class="sheep-project none">No project</span>
							{/if}
						</div>
						<span class="badge badge-{s.status}">{s.status}</span>
					</div>
					<div class="sheep-meta">
						<select class="input provider-select" value={s.provider} aria-label="Provider for {s.name}"
							onchange={(e) => changeProvider(s.name, e.target.value)}>
							<option value="claude">Claude</option>
							<option value="opencode">OpenCode</option>
							<option value="pi">Pi</option>
							<option value="grok">Grok</option>
							<option value="embedded">Embedded</option>
							<option value="magi">MAGI 🧠</option>
							<option value="auto">Auto</option>
						</select>
						<button class="btn btn-sm btn-ghost btn-icon del" onclick={() => removeSheep(s.name)} title="Remove" aria-label="Remove {s.name}">
							<Icon name="trash" size={14} />
						</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.page { max-width: var(--content-max); }

	.add-form {
		margin-bottom: 20px;
		padding: 18px 20px;
		border-color: var(--accent-line);
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.form-row {
		display: grid;
		grid-template-columns: 1fr 1fr auto;
		align-items: end;
		gap: 12px;
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.form-group label {
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		color: var(--text-secondary);
	}

	.form-group .input {
		width: 100%;
		font-size: var(--fs-sm);
	}

	.sheep-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
		gap: 12px;
	}

	.sheep-card {
		display: flex;
		flex-direction: column;
		gap: 14px;
		padding: 14px;
	}

	/* .working / .error bevels come from app.css (.card.working, .card.error) */

	.sheep-header {
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.ava {
		display: grid;
		place-items: center;
		width: 32px;
		height: 32px;
		flex-shrink: 0;
		margin: 2px;
		border-radius: 0;
		background: var(--p-dgray);
		box-shadow: var(--px-ring-2);
		color: var(--text-primary);
		font-weight: var(--fw-semibold);
		font-size: var(--fs-sm);
	}

	.working .ava {
		background: var(--live);
		color: #000;
	}

	.who {
		display: flex;
		flex-direction: column;
		min-width: 0;
		flex: 1;
	}

	.sheep-name {
		font-weight: var(--fw-semibold);
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.sheep-project {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	a.sheep-project:hover {
		color: var(--accent);
		text-decoration: none;
	}

	.sheep-project.none {
		font-family: var(--font-sans);
		font-style: normal;
	}

	.sheep-meta {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.provider-select {
		flex: 1;
		min-width: 0;
		height: 30px;
		padding-top: 0;
		padding-bottom: 0;
		font-size: var(--fs-xs);
	}

	.del:hover {
		color: var(--danger-text);
		background: var(--danger-soft);
	}

	@media (max-width: 768px) {
		.form-row {
			grid-template-columns: 1fr;
		}
	}
</style>
