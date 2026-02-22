<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPost, apiDelete } from '$lib/api.js';

	let projects = [];
	let loaded = false;
	let showAdd = false;
	let newName = '';
	let newPath = '';
	let newDesc = '';
	let adding = false;

	onMount(() => loadProjects());

	async function loadProjects() {
		const res = await apiGet('/api/projects');
		if (res?.data) projects = res.data;
		loaded = true;
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
		<h1>Projects</h1>
		<button class="btn btn-primary" onclick={() => showAdd = !showAdd}>
			{showAdd ? 'Cancel' : '+ Add Project'}
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
				{adding ? 'Adding...' : 'Add'}
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
		<div class="project-list">
			{#each projects as p}
				<div class="card project-item">
					<div class="project-header">
						<div>
							<a href="/projects/{encodeURIComponent(p.name)}" class="project-name-link">{p.name}</a>
							{#if p.sheep}
								<span class="badge badge-idle">{p.sheep}</span>
							{/if}
						</div>
						<div class="project-actions">
							<a href="/projects/{encodeURIComponent(p.name)}" class="btn btn-sm">Open</a>
							<button class="btn btn-danger btn-sm" onclick={() => removeProject(p.name)}>Remove</button>
						</div>
					</div>
					<div class="project-path mono">{p.path}</div>
					{#if p.description}
						<div class="project-desc">{p.description}</div>
					{/if}
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

	.page-header h1 {
		font-size: 24px;
		font-weight: 600;
	}

	.text-muted { color: var(--text-secondary); }

	.add-form {
		margin-bottom: 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.form-row {
		display: grid;
		grid-template-columns: 1fr 2fr;
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

	.project-list {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.project-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.project-name-link {
		font-weight: 600;
		margin-right: 8px;
		color: var(--text-primary);
		text-decoration: none;
	}

	.project-name-link:hover {
		color: var(--accent);
		text-decoration: none;
	}

	.project-actions {
		display: flex;
		gap: 6px;
		align-items: center;
	}

	.project-path {
		font-size: 12px;
		color: var(--text-secondary);
		margin-top: 4px;
	}

	.project-desc {
		font-size: 13px;
		color: var(--text-secondary);
		margin-top: 2px;
	}

	.btn-sm {
		padding: 2px 10px;
		font-size: 12px;
	}

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	@media (max-width: 768px) {
		.form-row {
			grid-template-columns: 1fr;
		}

		.project-header {
			flex-direction: column;
			align-items: flex-start;
			gap: 8px;
		}

		.project-actions {
			width: 100%;
		}

		.project-actions .btn-sm {
			padding: 6px 12px;
		}

		.page-header h1 {
			font-size: 20px;
		}
	}
</style>
