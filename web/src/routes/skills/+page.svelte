<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { projects } from '$lib/stores.js';
	import SkillForm from '$lib/components/SkillForm.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Tabs from '$lib/components/Tabs.svelte';
	import Icon from '$lib/components/Icon.svelte';

	let skills = $state([]);
	let loaded = $state(false);
	let unsubs = [];

	// Create/Edit modal
	let showForm = $state(false);
	let editingSkill = $state(null);

	// Filter
	let filterScope = $state('all');
	let query = $state('');

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

	let filteredSkills = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return skills.filter((sk) =>
			(filterScope === 'all' || sk.scope === filterScope) &&
			(!q || [sk.name, sk.description, sk.project, ...(sk.tags || [])].some((v) => (v || '').toLowerCase().includes(q)))
		);
	});

	let scopeTabs = $derived([
		{ value: 'all', label: 'All', count: skills.length },
		{ value: 'global', label: 'Global', count: skills.filter((s) => s.scope === 'global').length },
		{ value: 'project', label: 'Project', count: skills.filter((s) => s.scope === 'project').length }
	]);

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
	<PageHeader
		eyebrow="Configure · Know-how"
		title="Skills"
		count={skills.length}
		subtitle="Reusable instructions the flock loads on demand — checklists, procedures, house rules. Global skills reach every project."
	>
		<button class="btn" onclick={handleImport}>
			<Icon name="arrow-up-right" size={13} /> Import .md
		</button>
		<button class="btn btn-primary" onclick={openCreate}>
			<Icon name="plus" size={14} stroke={2.2} /> New skill
		</button>
	</PageHeader>

	<div class="toolbar">
		<Tabs tabs={scopeTabs} value={filterScope} onChange={(v) => (filterScope = v)} ariaLabel="Scope filter" />
		<input class="input input-search search" type="search" bind:value={query} placeholder="Search skills, tags, projects…" aria-label="Search skills" />
	</div>

	{#if !loaded}
		<div class="skill-list" aria-busy="true">
			{#each Array(9) as _}
				<div class="card skill-item"><div class="skeleton" style="height: 14px; width: 50%"></div><div class="skeleton" style="height: 10px; width: 90%; margin-top: 16px"></div><div class="skeleton" style="height: 10px; width: 70%; margin-top: 8px"></div></div>
			{/each}
		</div>
	{:else if filteredSkills.length === 0 && !showForm}
		<div class="card">
			<EmptyState icon="list" title={query ? 'Nothing matches' : 'No skills yet'} description={query ? `No skill mentions “${query}”.` : 'Create a skill to teach the flock a procedure once and reuse it everywhere.'} />
		</div>
	{:else}
		<div class="skill-list">
			{#each filteredSkills as sk, i (sk.id)}
				<article class="card skill-item reveal" class:disabled={!sk.enabled} style="--i: {i}">
					<div class="skill-header">
						<span class="skill-name mono">{sk.name}</span>
						<label class="enable" title={sk.enabled ? 'Disable' : 'Enable'}>
							<input type="checkbox" class="switch" checked={sk.enabled} onchange={() => toggleEnabled(sk)} aria-label={sk.enabled ? `Disable ${sk.name}` : `Enable ${sk.name}`} />
						</label>
					</div>
					<div class="badges">
						<span class="chip" class:chip-accent={sk.scope === 'global'}>{sk.scope}</span>
						{#if sk.bundled}
							<span class="chip chip-queued">bundled</span>
						{/if}
						{#if sk.project}
							<span class="chip chip-mono">{sk.project}</span>
						{/if}
					</div>
					<p class="skill-desc">{sk.description || truncate(sk.content, 160)}</p>
					{#if sk.tags?.length > 0}
						<div class="skill-tags">
							{#each sk.tags as tag}
								<span class="tag">#{tag}</span>
							{/each}
						</div>
					{/if}
					<div class="skill-actions">
						<button class="btn btn-xs btn-ghost" onclick={() => openEdit(sk)}><Icon name="edit" size={12} /> Edit</button>
						<button class="btn btn-xs btn-ghost" onclick={() => exportSkill(sk)} title="Export as .md"><Icon name="arrow-up-right" size={12} /> Export</button>
						{#if !sk.bundled}
							<button class="btn btn-xs btn-ghost del" onclick={() => deleteSkill(sk)}><Icon name="trash" size={12} /> Delete</button>
						{/if}
					</div>
				</article>
			{/each}
		</div>
	{/if}

	<!-- Create/Edit Modal — only Close/Cancel closes (no outside-click dismiss) -->
	{#if showForm}
		<div class="modal-overlay">
			<div class="modal-content card" role="dialog" aria-modal="true" aria-label={editingSkill ? 'Edit skill' : 'New skill'}>
				<div class="modal-header">
					<h2>{editingSkill ? 'Edit skill' : 'New skill'}</h2>
					<button class="btn btn-sm btn-ghost btn-icon" onclick={closeForm} aria-label="Close"><Icon name="x" size={15} /></button>
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
	.page { max-width: var(--content-max); }

	.toolbar {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-wrap: wrap;
		margin-bottom: 18px;
	}

	.search {
		flex: 1;
		min-width: 200px;
		max-width: 380px;
		margin-left: auto;
		padding-top: 7px;
		padding-bottom: 7px;
		font-size: var(--fs-sm);
	}

	.skill-list {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
		gap: 12px;
	}

	.skill-item {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 16px 16px 10px;
		transition: border-color var(--dur-1) steps(2), opacity var(--dur-2) steps(2);
	}

	.skill-item:hover {
		border-color: var(--border-strong);
	}

	.skill-item.disabled {
		opacity: 0.55;
	}

	.skill-item.disabled:hover {
		opacity: 0.9;
	}

	.skill-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
	}

	.skill-name {
		min-width: 0;
		font-size: var(--fs-sm);
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.enable {
		display: inline-flex;
		cursor: pointer;
	}

	.badges {
		display: flex;
		gap: 6px;
		flex-wrap: wrap;
	}

	.skill-desc {
		font-size: var(--fs-xs);
		line-height: 1.55;
		color: var(--text-secondary);
		display: -webkit-box;
		-webkit-line-clamp: 3;
		-webkit-box-orient: vertical;
		overflow: hidden;
		word-break: break-word;
	}

	.skill-tags {
		display: flex;
		gap: 4px 10px;
		flex-wrap: wrap;
	}

	.tag {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.skill-actions {
		display: flex;
		gap: 2px;
		margin: auto -6px 0;
		padding-top: 8px;
		border-top: 1px solid var(--border-subtle);
		opacity: 0.55;
		transition: opacity var(--dur-1) steps(2);
	}

	.skill-item:hover .skill-actions,
	.skill-item:focus-within .skill-actions {
		opacity: 1;
	}

	.del:hover {
		color: var(--danger-text);
		background: var(--danger-soft);
	}

	.modal-content {
		max-width: 760px;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 18px;
	}

	.modal-header h2 {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
	}

	@media (max-width: 768px) {
		.search {
			max-width: none;
			margin-left: 0;
		}

		.skill-list {
			grid-template-columns: 1fr;
		}

		.skill-actions {
			opacity: 1;
		}
	}
</style>
