<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { projects } from '$lib/stores.js';
	import SkillForm from '$lib/components/SkillForm.svelte';

	let skills = $state([]);
	let loaded = $state(false);
	let unsubs = [];

	// Create/Edit modal
	let showForm = $state(false);
	let editingSkill = $state(null);

	// Filter
	let filterScope = $state('all');

	onMount(async () => {
		await loadSkills();

		unsubs.push(onSSE('skill_created', () => loadSkills()));
		unsubs.push(onSSE('skill_updated', () => loadSkills()));
		unsubs.push(onSSE('skill_deleted', () => loadSkills()));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	async function loadSkills() {
		const res = await apiGet('/api/skills');
		if (res?.data) {
			skills = res.data;
		}
		loaded = true;
	}

	let filteredSkills = $derived(
		filterScope === 'all'
			? skills
			: skills.filter(sk => sk.scope === filterScope)
	);

	function openCreate() {
		editingSkill = null;
		showForm = true;
	}

	function openEdit(sk) {
		editingSkill = sk;
		showForm = true;
	}

	function closeForm() {
		showForm = false;
		editingSkill = null;
	}

	async function handleSave(data) {
		if (editingSkill) {
			const res = await apiPatch(`/api/skills/${editingSkill.id}`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to update');
		} else {
			let res;
			if (data.scope === 'global') {
				res = await apiPost('/api/skills', data);
			} else {
				res = await apiPost(`/api/projects/${encodeURIComponent(data.project)}/skills`, data);
			}
			if (!res?.success) throw new Error(res?.message || 'Failed to create');
		}
		closeForm();
		await loadSkills();
	}

	async function toggleEnabled(sk) {
		await apiPatch(`/api/skills/${sk.id}`, { enabled: !sk.enabled });
		await loadSkills();
	}

	async function deleteSkill(sk) {
		if (!confirm(`Delete skill "${sk.name}"?`)) return;
		await apiDelete(`/api/skills/${sk.id}`);
		await loadSkills();
	}

	async function exportSkill(sk) {
		const res = await apiGet(`/api/skills/${sk.id}/export`, { raw: true });
		if (res) {
			const blob = new Blob([res], { type: 'text/markdown' });
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = `${sk.name}.md`;
			a.click();
			URL.revokeObjectURL(url);
		}
	}

	async function handleImport() {
		const input = document.createElement('input');
		input.type = 'file';
		input.accept = '.md';
		input.onchange = async (e) => {
			const file = e.target.files[0];
			if (!file) return;
			const content = await file.text();
			const res = await apiPost('/api/skills/import', { content });
			if (res?.success) {
				await loadSkills();
			}
		};
		input.click();
	}

	function truncate(str, max) {
		if (!str) return '';
		return str.length > max ? str.substring(0, max) + '...' : str;
	}
</script>

<div class="page">
	<div class="page-header">
		<h1 class="page-title">Skills</h1>
		<div class="header-actions">
			<button class="btn" onclick={handleImport}>Import .md</button>
			<button class="btn btn-primary" onclick={openCreate}>+ New Skill</button>
		</div>
	</div>

	<!-- Filter -->
	<div class="filter-bar">
		<button class="filter-btn" class:active={filterScope === 'all'} onclick={() => filterScope = 'all'}>All</button>
		<button class="filter-btn" class:active={filterScope === 'global'} onclick={() => filterScope = 'global'}>Global</button>
		<button class="filter-btn" class:active={filterScope === 'project'} onclick={() => filterScope = 'project'}>Project</button>
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if filteredSkills.length === 0 && !showForm}
		<div class="card empty-state">
			<p>No skills found.</p>
			<p class="text-muted">Create a skill to enhance AI capabilities for your projects.</p>
		</div>
	{:else}
		<div class="skill-list">
			{#each filteredSkills as sk (sk.id)}
				<div class="card skill-item" class:disabled={!sk.enabled}>
					<div class="skill-header">
						<div class="skill-name-row">
							<span class="skill-name">{sk.name}</span>
							<span class="badge" class:global={sk.scope === 'global'}>{sk.scope}</span>
							{#if sk.bundled}
								<span class="badge bundled">bundled</span>
							{/if}
						</div>
						<div class="skill-actions">
							<button class="btn btn-sm" onclick={() => toggleEnabled(sk)}>
								{sk.enabled ? 'Disable' : 'Enable'}
							</button>
							<button class="btn btn-sm" onclick={() => openEdit(sk)}>Edit</button>
							<button class="btn btn-sm" onclick={() => exportSkill(sk)} title="Export as .md">Export</button>
							{#if !sk.bundled}
								<button class="btn btn-sm btn-danger" onclick={() => deleteSkill(sk)}>Delete</button>
							{/if}
						</div>
					</div>
					{#if sk.description}
						<div class="skill-desc">{sk.description}</div>
					{/if}
					<div class="skill-content-preview">{truncate(sk.content, 80)}</div>
					<div class="skill-meta">
						{#if sk.project}
							<span class="meta-project">{sk.project}</span>
						{/if}
						{#if sk.tags?.length > 0}
							<span class="skill-tags">
								{#each sk.tags as tag}
									<span class="tag">{tag}</span>
								{/each}
							</span>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Create/Edit Modal -->
	{#if showForm}
		<div class="modal-overlay" onclick={closeForm}>
			<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
				<div class="modal-header">
					<h2>{editingSkill ? 'Edit Skill' : 'New Skill'}</h2>
					<button class="btn" onclick={closeForm}>Close</button>
				</div>
				<SkillForm
					skill={editingSkill}
					projects={$projects}
					onSave={handleSave}
					onCancel={closeForm}
				/>
			</div>
		</div>
	{/if}
</div>

<style>
	.page-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 16px;
	}

	.page-title { font-size: 20px; font-weight: 600; }
	.text-muted { color: var(--text-secondary); }

	.header-actions {
		display: flex;
		gap: 8px;
	}

	.btn-primary {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.filter-bar {
		display: flex;
		gap: 4px;
		margin-bottom: 16px;
	}

	.filter-btn {
		padding: 4px 12px;
		border: 1px solid var(--border);
		background: var(--bg-primary);
		color: var(--text-secondary);
		border-radius: 12px;
		cursor: pointer;
		font-size: 12px;
		transition: all 0.15s;
	}

	.filter-btn:hover {
		border-color: var(--accent);
		color: var(--accent);
	}

	.filter-btn.active {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.skill-list {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
		gap: 8px;
	}

	.skill-item {
		transition: border-color 0.15s;
	}

	.skill-item:hover {
		border-color: var(--accent);
	}

	.skill-item.disabled {
		opacity: 0.5;
	}

	.skill-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 6px;
		gap: 8px;
	}

	.skill-name-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		min-width: 0;
	}

	.skill-name {
		font-weight: 600;
		font-size: 14px;
	}

	.badge {
		font-size: 10px;
		padding: 2px 6px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
		font-weight: 600;
	}

	.badge.global {
		background: rgba(56, 139, 253, 0.15);
		color: var(--accent);
	}

	.badge.bundled {
		background: rgba(163, 113, 247, 0.15);
		color: #a371f7;
	}

	.skill-actions {
		display: flex;
		gap: 4px;
		flex-shrink: 0;
	}

	.btn-sm {
		padding: 3px 8px;
		font-size: 11px;
	}

	.btn-danger {
		color: var(--danger);
		border-color: var(--danger);
	}

	.btn-danger:hover {
		background: rgba(248, 81, 73, 0.1);
	}

	.skill-desc {
		font-size: 13px;
		color: var(--text-secondary);
		margin-bottom: 4px;
	}

	.skill-content-preview {
		font-size: 12px;
		color: var(--text-primary);
		margin-bottom: 6px;
		line-height: 1.4;
		font-family: var(--font-mono);
		opacity: 0.7;
	}

	.skill-meta {
		display: flex;
		gap: 12px;
		font-size: 12px;
		color: var(--text-secondary);
		align-items: center;
	}

	.meta-project {
		color: var(--accent);
	}

	.skill-tags {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
	}

	.tag {
		font-size: 10px;
		padding: 1px 6px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
	}

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	/* Modal */
	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
		padding: 20px;
	}

	.modal-content {
		width: 100%;
		max-width: 700px;
		max-height: 85vh;
		overflow-y: auto;
		padding: 24px;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 20px;
	}

	.modal-header h2 { font-size: 18px; }

	@media (max-width: 768px) {
		.page-header {
			flex-direction: column;
			align-items: flex-start;
			gap: 12px;
		}

		.skill-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.skill-actions {
			flex-wrap: wrap;
		}

		.skill-meta {
			flex-wrap: wrap;
			gap: 6px;
		}

		.modal-content {
			padding: 16px;
			max-height: 90vh;
		}
	}
</style>
