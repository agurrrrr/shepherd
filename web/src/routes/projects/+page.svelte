<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { sheep, projects as projectsStore } from '$lib/stores.js';
	import { onSSE } from '$lib/sse.js';
	import Icon from '$lib/components/Icon.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';

	let projects = $state([]);
	let loaded = $state(false);
	let showAdd = $state(false);
	let newName = $state('');
	let newPath = $state('');
	let newDesc = $state('');
	let adding = $state(false);
	let query = $state('');
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
		if (projRes?.data) {
			projects = projRes.data;
			// Keep the sidebar in sync — it renders from this store.
			projectsStore.set(projRes.data);
		}
		if (sheepRes?.data) sheep.set(sheepRes.data);
		loaded = true;
	}

	async function refreshSheep() {
		const res = await apiGet('/api/sheep');
		if (res?.data) sheep.set(res.data);
	}

	function sheepFor(projectName) {
		return ($sheep || []).find(s => s.project === projectName);
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

	async function toggleHidden(name, hidden) {
		await apiPatch(`/api/projects/${encodeURIComponent(name)}`, { hidden });
		await loadProjects();
	}

	async function removeProject(name) {
		if (!confirm(`Remove project "${name}"?`)) return;
		await apiDelete(`/api/projects/${encodeURIComponent(name)}`);
		await loadProjects();
	}

	let filtered = $derived.by(() => {
		const q = query.trim().toLowerCase();
		if (!q) return projects;
		return projects.filter((p) =>
			[p.name, p.description, p.path, p.sheep].some((v) => (v || '').toLowerCase().includes(q))
		);
	});

	let workingCount = $derived(projects.filter((p) => sheepFor(p.name)?.status === 'working').length);
	let hiddenCount = $derived(projects.filter((p) => p.hidden).length);
</script>

<div class="page">
	<PageHeader
		eyebrow="Watch · Pastures"
		title="Projects"
		count={projects.length}
		subtitle={`Every repository the flock tends${workingCount ? ` — ${workingCount} being worked on right now` : ''}. Hidden projects (${hiddenCount}) stay here but leave the sidebar.`}
	>
		<input
			class="input input-search filter"
			type="search"
			bind:value={query}
			placeholder="Filter projects…"
			aria-label="Filter projects"
		/>
		<button class="btn" class:btn-primary={!showAdd} onclick={() => showAdd = !showAdd} aria-expanded={showAdd}>
			<Icon name={showAdd ? 'x' : 'plus'} size={14} stroke={2.2} />
			{showAdd ? 'Cancel' : 'Add project'}
		</button>
	</PageHeader>

	{#if showAdd}
		<form class="card add-form" onsubmit={(e) => { e.preventDefault(); addProject(); }}>
			<div class="add-head">
				<h2 class="add-title">A new pasture</h2>
				<p class="text-muted">Point Shepherd at a directory on this machine. A sheep can be assigned afterwards.</p>
			</div>
			<div class="form-row">
				<div class="form-group">
					<label for="np-name">Name</label>
					<input id="np-name" class="input" bind:value={newName} placeholder="my-project" />
				</div>
				<div class="form-group">
					<label for="np-path">Path</label>
					<input id="np-path" class="input mono" bind:value={newPath} placeholder="/home/user/code/my-project" />
				</div>
			</div>
			<div class="form-group">
				<label for="np-desc">Description</label>
				<input id="np-desc" class="input" bind:value={newDesc} placeholder="Optional description" />
			</div>
			<div class="add-actions">
				<button type="submit" class="btn btn-primary" disabled={adding || !newName || !newPath}>
					{adding ? 'Adding…' : 'Create project'}
				</button>
			</div>
		</form>
	{/if}

	{#if !loaded}
		<div class="project-grid" aria-busy="true">
			{#each Array(9) as _}
				<div class="card project-card">
					<div class="skeleton" style="height: 14px; width: 45%"></div>
					<div class="skeleton" style="height: 10px; width: 30%; margin-top: 10px"></div>
					<div class="skeleton" style="height: 10px; width: 70%; margin-top: 18px"></div>
				</div>
			{/each}
		</div>
	{:else if projects.length === 0}
		<div class="card">
			<EmptyState icon="sheep" title="No pastures yet" description="Add a project to give the flock somewhere to work." />
		</div>
	{:else if filtered.length === 0}
		<div class="card">
			<EmptyState icon="list" title="Nothing matches" description={`No project name, path or description contains “${query}”.`} />
		</div>
	{:else}
		<div class="project-grid">
			{#each filtered as p, i (p.name)}
				{@const sh = sheepFor(p.name)}
				{@const status = sh?.status || 'idle'}
				<a
					href="/projects/{encodeURIComponent(p.name)}"
					class="project-card card reveal"
					class:working={status === 'working'}
					class:error={status === 'error'}
					class:hidden-project={p.hidden}
					style="--i: {i}"
				>
					<div class="card-top">
						<span class="project-name">{p.name}</span>
						<div class="card-actions">
							<button
								class="act"
								class:pinned={p.hidden}
								title={p.hidden ? 'Show in sidebar' : 'Hide from sidebar'}
								aria-label={p.hidden ? 'Show in sidebar' : 'Hide from sidebar'}
								onclick={(e) => { e.preventDefault(); e.stopPropagation(); toggleHidden(p.name, !p.hidden); }}
							><Icon name={p.hidden ? 'eye-off' : 'eye'} size={14} /></button>
							<button
								class="act act-danger"
								title="Remove"
								aria-label="Remove {p.name}"
								onclick={(e) => { e.preventDefault(); e.stopPropagation(); removeProject(p.name); }}
							><Icon name="trash" size={14} /></button>
						</div>
					</div>

					<div class="sheep-row">
						{#if p.sheep}
							<span class="sheep-ava" aria-hidden="true">{[...p.sheep][0]}</span>
							<span class="sheep-name">{p.sheep}</span>
							{#if sh?.provider}<span class="provider mono">{sh.provider}</span>{/if}
						{:else}
							<span class="sheep-name unassigned">No sheep</span>
						{/if}
						{#if status === 'working'}
							<span class="state live"><span class="live-dot"></span>Working</span>
						{:else if status === 'error'}
							<span class="state bad">Error</span>
						{:else if p.hidden}
							<span class="state">Hidden</span>
						{/if}
					</div>

					{#if p.description}
						<p class="project-desc">{p.description}</p>
					{/if}
					<span class="project-path mono" title={p.path}>{p.path}</span>
					<span class="go" aria-hidden="true"><Icon name="arrow-up-right" size={14} /></span>
				</a>
			{/each}
		</div>
	{/if}
</div>

<style>
	.page { max-width: var(--content-max); }

	.filter {
		width: 220px;
		padding-top: 6px;
		padding-bottom: 6px;
		font-size: var(--fs-sm);
	}

	/* Add form */
	.add-form {
		margin-bottom: var(--space-6);
		padding: 22px;
		display: flex;
		flex-direction: column;
		gap: 14px;
		border-color: var(--accent-line);
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.add-head {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-bottom: 4px;
	}

	.add-title {
		font-family: var(--font-display);
		font-size: 28px;
		font-weight: 400;
	}

	.add-head p {
		font-size: var(--fs-sm);
	}

	.form-row {
		display: grid;
		grid-template-columns: 1fr 2fr;
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

	.form-group .input { width: 100%; font-size: var(--fs-sm); }

	.add-actions {
		display: flex;
		justify-content: flex-end;
	}

	/* Grid */
	.project-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
		gap: 12px;
	}

	.project-card {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 8px;
		min-height: 128px;
		padding: 16px 16px 14px;
		overflow: hidden;
	}

	.project-card:hover {
		transform: translateY(-2px);
		box-shadow: var(--sheen), var(--shadow-2);
	}

	.project-card.working {
		border-color: var(--live-line);
		background:
			radial-gradient(100% 80% at 0% 0%, rgba(95, 224, 194, 0.09), transparent 60%),
			var(--bg-2);
	}

	.project-card.error {
		border-color: var(--danger-line);
	}

	.card-top {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 8px;
	}

	.project-name {
		min-width: 0;
		font-weight: var(--fw-semibold);
		font-size: 15px;
		letter-spacing: -0.012em;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.card-actions {
		display: flex;
		align-items: center;
		gap: 2px;
		margin-right: -6px;
	}

	.act {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border: none;
		border-radius: 7px;
		background: none;
		color: var(--text-tertiary);
		cursor: pointer;
		opacity: 0;
		transition: opacity var(--dur-1) ease, color var(--dur-1) ease, background var(--dur-1) ease;
	}

	.project-card:hover .act,
	.project-card:focus-within .act,
	.act:focus-visible {
		opacity: 1;
	}

	.act:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.act-danger:hover {
		color: var(--danger);
		background: var(--danger-soft);
	}

	/* Hidden projects keep the toggle visible so it can be undone at a glance. */
	.act.pinned {
		opacity: 1;
		color: var(--accent);
	}

	.sheep-row {
		display: flex;
		align-items: center;
		gap: 7px;
		min-width: 0;
		font-size: var(--fs-xs);
	}

	.sheep-ava {
		display: grid;
		place-items: center;
		width: 18px;
		height: 18px;
		flex-shrink: 0;
		border-radius: 50%;
		background: var(--wool-soft);
		color: var(--wool);
		font-size: 10px;
		font-weight: var(--fw-semibold);
	}

	.working .sheep-ava {
		background: var(--live-soft);
		color: var(--live);
	}

	.sheep-name {
		color: var(--text-secondary);
		white-space: nowrap;
	}

	.sheep-name.unassigned {
		color: var(--text-tertiary);
		font-style: italic;
	}

	.provider {
		font-size: 10px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.state {
		margin-left: auto;
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.state.live { color: var(--live); }
	.state.bad { color: var(--danger); }

	.project-desc {
		font-size: var(--fs-xs);
		line-height: 1.5;
		color: var(--text-secondary);
		margin: 0;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.project-path {
		margin-top: auto;
		padding-right: 22px;
		font-size: 10.5px;
		color: var(--text-tertiary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.go {
		position: absolute;
		right: 14px;
		bottom: 12px;
		color: var(--text-tertiary);
		opacity: 0;
		transform: translate(-4px, 4px);
		transition: opacity var(--dur-2) ease, transform var(--dur-2) var(--ease-out), color var(--dur-1) ease;
	}

	.project-card:hover .go {
		opacity: 1;
		transform: none;
		color: var(--accent);
	}

	.hidden-project {
		opacity: 0.5;
	}

	.hidden-project:hover {
		opacity: 1;
	}

	@media (max-width: 768px) {
		.filter {
			flex: 1;
			width: auto;
		}

		.form-row {
			grid-template-columns: 1fr;
		}

		.project-grid {
			grid-template-columns: 1fr;
		}

		.act {
			opacity: 1;
		}
	}
</style>
