<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiDelete } from '$lib/api.js';
	import { sheep } from '$lib/stores.js';
	import { onSSE } from '$lib/sse.js';

	let projects = [];
	let loaded = false;
	let showAdd = false;
	let newName = '';
	let newPath = '';
	let newDesc = '';
	let adding = false;
	let unsubs = [];

	onMount(async () => {
		await loadProjects();
		unsubs.push(onSSE('status_change', refreshSheep));
	});

	onDestroy(() => unsubs.forEach(fn => fn?.()));

	async function loadProjects() {
		const [projRes, sheepRes] = await Promise.all([
			apiGet('/api/projects'),
			apiGet('/api/sheep')
		]);
		if (projRes?.data) projects = projRes.data;
		if (sheepRes?.data) sheep.set(sheepRes.data);
		loaded = true;
	}

	async function refreshSheep() {
		const res = await apiGet('/api/sheep');
		if (res?.data) sheep.set(res.data);
	}

	function getSheepStatus(projectName) {
		const list = $sheep || [];
		const s = list.find(s => s.project === projectName);
		return s?.status || 'idle';
	}

	async function addProject() {
		if (!newName || !newPath) return;
		adding = true;
		const res = await apiPost('/api/projects', {
			name: newName,
			path: newPath,
			description: newDesc
		});
		if (res?.success) {
			newName = '';
			newPath = '';
			newDesc = '';
			showAdd = false;
			await loadProjects();
		}
		adding = false;
	}

	async function removeProject(name) {
		if (!confirm(`Remove project "${name}"?`)) return;
		await apiDelete(`/api/projects/${encodeURIComponent(name)}`);
		await loadProjects();
	}
</script>

<div class="page">
	<div class="page-header">
		<div class="page-header-left">
			<h1>Projects</h1>
			<span class="count-badge">{projects.length}</span>
		</div>
		<button class="btn btn-primary" onclick={() => showAdd = !showAdd}>
			{showAdd ? 'Cancel' : '+ Add'}
		</button>
	</div>

	{#if showAdd}
		<div class="card add-form">
			<div class="form-row">
				<div class="form-group">
					<label>Name</label>
					<input class="input" bind:value={newName} placeholder="my-project" />
				</div>
				<div class="form-group">
					<label>Path</label>
					<input class="input" bind:value={newPath} placeholder="/home/user/code/my-project" />
				</div>
			</div>
			<div class="form-group">
				<label>Description</label>
				<input class="input" bind:value={newDesc} placeholder="Optional description" />
			</div>
			<button class="btn btn-primary" onclick={addProject} disabled={adding || !newName || !newPath}>
				{adding ? 'Adding...' : 'Create Project'}
			</button>
		</div>
	{/if}

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if projects.length === 0}
		<div class="card empty-state">
			<p>No projects yet.</p>
			<p class="text-muted">Add a project to get started.</p>
		</div>
	{:else}
		<div class="project-grid">
			{#each projects as p}
				{@const status = getSheepStatus(p.name)}
				<a href="/projects/{encodeURIComponent(p.name)}" class="project-card card" class:working={status === 'working'}>
					<div class="card-top">
						<div class="card-name-row">
							<span class="status-dot {status}"></span>
							<span class="project-name">{p.name}</span>
						</div>
						<button
							class="btn-remove"
							title="Remove"
							onclick={(e) => { e.preventDefault(); e.stopPropagation(); removeProject(p.name); }}
						>&times;</button>
					</div>
					{#if p.sheep}
						<span class="sheep-tag">{p.sheep}</span>
					{:else}
						<span class="sheep-tag unassigned">no sheep</span>
					{/if}
					{#if p.description}
						<p class="project-desc">{p.description}</p>
					{/if}
					<span class="project-path">{p.path}</span>
				</a>
			{/each}
		</div>
	{/if}
</div>

<style>
	.page { max-width: 1200px; }

	.page-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 16px;
	}

	.page-header-left {
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.page-header h1 { font-size: 20px; font-weight: 600; }

	.count-badge {
		font-size: 12px;
		font-family: var(--font-mono);
		background: var(--bg-tertiary);
		padding: 2px 8px;
		border-radius: 10px;
		color: var(--text-secondary);
	}

	.text-muted { color: var(--text-secondary); }

	.add-form {
		margin-bottom: 16px;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}

	.form-row {
		display: grid;
		grid-template-columns: 1fr 2fr;
		gap: 10px;
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.form-group label {
		font-size: 12px;
		color: var(--text-secondary);
	}

	.form-group .input { width: 100%; }

	/* Project Grid */
	.project-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
		gap: 10px;
	}

	.project-card {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 12px 14px;
		text-decoration: none;
		color: inherit;
		transition: border-color 0.15s;
	}

	.project-card:hover {
		border-color: var(--accent);
		text-decoration: none;
		color: inherit;
	}

	.project-card.working {
		border-color: var(--accent);
	}

	.card-top {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.card-name-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.project-name {
		font-weight: 600;
		font-size: 14px;
	}

	/* Status dot */
	.status-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.status-dot.idle { background: var(--text-secondary); }
	.status-dot.working {
		background: var(--accent);
		animation: pulse 1.5s ease-in-out infinite;
	}
	.status-dot.error { background: var(--danger); }

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	.btn-remove {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 16px;
		cursor: pointer;
		padding: 0 4px;
		opacity: 0;
		transition: opacity 0.15s, color 0.15s;
	}

	.project-card:hover .btn-remove {
		opacity: 1;
	}

	.btn-remove:hover {
		color: var(--danger);
	}

	.sheep-tag {
		font-size: 11px;
		color: var(--accent);
		font-family: var(--font-mono);
	}

	.sheep-tag.unassigned {
		color: var(--text-secondary);
		font-style: italic;
	}

	.project-desc {
		font-size: 12px;
		color: var(--text-secondary);
		margin: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.project-path {
		font-size: 11px;
		color: var(--text-secondary);
		font-family: var(--font-mono);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		opacity: 0.6;
	}

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	@media (max-width: 768px) {
		.form-row {
			grid-template-columns: 1fr;
		}

		.project-grid {
			grid-template-columns: 1fr;
		}

		.btn-remove {
			opacity: 1;
		}
	}
</style>
