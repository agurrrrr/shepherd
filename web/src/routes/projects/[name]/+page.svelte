<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import CommandInput from '$lib/components/CommandInput.svelte';
	import Pagination from '$lib/components/Pagination.svelte';
	import GitGraph from '$lib/components/GitGraph.svelte';
	import ScheduleForm from '$lib/components/ScheduleForm.svelte';
	import SkillForm from '$lib/components/SkillForm.svelte';
	import FileBrowser from '$lib/components/FileBrowser.svelte';
	import SpecViewer from '$lib/components/SpecViewer.svelte';

	let projectName = $state('');
	let project = $state(null);
	let loading = $state(true);
	let activeTab = $state('output');

	// Live output
	let liveOutput = $state([]);
	let sheepStatus = $state('idle');
	let sheepName = $state('');
	let sheepProvider = $state('claude');

	// Task history
	let tasks = $state([]);
	let taskPage = $state(1);
	let taskTotal = $state(0);
	let taskTotalPages = $state(1);
	let taskLimit = 10;
	let tasksLoaded = $state(false);
	let taskSearchQuery = $state('');

	// Documents
	let docs = $state([]);
	// Git
	let gitLoaded = $state(false);

	// Schedules
	let projectSchedules = $state([]);
	let schedulesLoaded = $state(false);
	let showScheduleForm = $state(false);
	let editingSchedule = $state(null);

	// Skills
	let projectSkills = $state([]);
	let skillsLoaded = $state(false);
	let showSkillForm = $state(false);
	let editingSkillItem = $state(null);

	// Files tab key — changes to force FileBrowser remount on project switch
	let filesKey = $state(0);

	let unsubs = [];

	// React to route param changes
	$effect(() => {
		const newName = decodeURIComponent($page.params.name);
		if (newName !== projectName) {
			projectName = newName;
			if (typeof window !== 'undefined') {
				resetAndLoad();
			}
		}
	});

	function resetAndLoad() {
		project = null;
		loading = true;
		liveOutput = [];
		sheepStatus = 'idle';
		sheepName = '';
		tasks = [];
		tasksLoaded = false;
		taskPage = 1;
		filesKey++; // force FileBrowser remount
		gitLoaded = false;
		projectSchedules = [];
		schedulesLoaded = false;
		showScheduleForm = false;
		editingSchedule = null;
		projectSkills = [];
		skillsLoaded = false;
		showSkillForm = false;
		editingSkillItem = null;
		loadProject();
	}

	onMount(() => {
		// SSE: live output (5000줄 버퍼, 멀티라인 분리)
		unsubs.push(onSSE('output', (data) => {
			if (data.project_name === projectName) {
				const lines = data.text.split('\n');
				liveOutput = [...liveOutput.slice(-(5000 - lines.length)), ...lines];
			}
		}));

		// SSE: status change
		unsubs.push(onSSE('status_change', (data) => {
			if (sheepName && data.sheep_name === sheepName) {
				sheepStatus = data.status;
			}
		}));

		// SSE: provider change (rate limit fallback / restore / manual)
		unsubs.push(onSSE('provider_change', (data) => {
			if (sheepName && data.sheep_name === sheepName) {
				sheepProvider = data.provider;
			}
		}));

		// SSE: task events -> refresh history
		unsubs.push(onSSE('task_complete', (data) => {
			if (data.project_name === projectName && tasksLoaded) loadTasks();
		}));
		unsubs.push(onSSE('task_fail', (data) => {
			if (data.project_name === projectName && tasksLoaded) loadTasks();
		}));
		unsubs.push(onSSE('task_start', (data) => {
			if (data.project_name === projectName) {
				// 새 작업 시작: 이전 출력 정리 후 프롬프트 표시
				liveOutput = [`▶ ${data.prompt}`, ''];
				if (tasksLoaded) loadTasks();
			}
		}));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	async function loadProject() {
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}`);
		if (res?.data) {
			project = res.data;
			sheepName = project.sheep || '';

			if (sheepName) {
				const sheepRes = await apiGet(`/api/sheep/${encodeURIComponent(sheepName)}`);
				if (sheepRes?.data) {
					sheepStatus = sheepRes.data.status;
					sheepProvider = sheepRes.data.provider || 'claude';
				}
			}

			await loadLatestOutput();
		}
		loading = false;
	}

	async function changeProvider(provider) {
		await apiPatch(`/api/sheep/${encodeURIComponent(sheepName)}/provider`, { provider });
		sheepProvider = provider;
	}

	async function loadLatestOutput() {
		const res = await apiGet(`/api/tasks?project=${encodeURIComponent(projectName)}&limit=1`);
		if (res?.data?.length > 0) {
			const taskRes = await apiGet(`/api/tasks/${res.data[0].id}`);
			if (taskRes?.data?.output) {
				liveOutput = taskRes.data.output;
			}
		}
	}

	async function loadTasks() {
		const params = new URLSearchParams();
		params.set('project', projectName);
		params.set('page', taskPage);
		params.set('limit', taskLimit);
		if (taskSearchQuery.trim()) params.set('q', taskSearchQuery.trim());
		const res = await apiGet(`/api/tasks?${params}`);
		if (res) {
			tasks = res.data || [];
			taskTotal = res.total || 0;
			taskTotalPages = res.total_pages || 1;
		}
		tasksLoaded = true;
	}

	function switchTab(tab) {
		activeTab = tab;
		if (tab === 'history' && !tasksLoaded) {
			loadTasks();
		}
		// Files tab: FileBrowser component manages its own state
		if (tab === 'git' && !gitLoaded) {
			gitLoaded = true;
		}
		if (tab === 'schedules' && !schedulesLoaded) {
			loadSchedules();
		}
		if (tab === 'skills' && !skillsLoaded) {
			loadProjectSkills();
		}
	}

	function onTaskPageChange(p) {
		taskPage = p;
		loadTasks();
	}

	function onTaskSearch(e) {
		if (e.key === 'Enter') {
			taskPage = 1;
			loadTasks();
		}
	}

	function truncate(s, max) {
		if (!s) return '';
		return s.length > max ? s.slice(0, max) + '...' : s;
	}

	function formatTime(ts) {
		if (!ts) return '';
		try {
			return new Date(ts).toLocaleString();
		} catch {
			return ts;
		}
	}

	// Schedule functions
	async function loadSchedules() {
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/schedules`);
		if (res?.data) {
			projectSchedules = res.data;
		}
		schedulesLoaded = true;
	}

	function openScheduleCreate() {
		editingSchedule = null;
		showScheduleForm = true;
	}

	function openScheduleEdit(sc) {
		editingSchedule = sc;
		showScheduleForm = true;
	}

	function closeScheduleForm() {
		showScheduleForm = false;
		editingSchedule = null;
	}

	async function handleScheduleSave(data) {
		if (editingSchedule) {
			const res = await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/schedules/${editingSchedule.id}`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to update');
		} else {
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/schedules`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to create');
		}
		closeScheduleForm();
		await loadSchedules();
	}

	async function toggleSchedule(sc) {
		await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/schedules/${sc.id}`, { enabled: !sc.enabled });
		await loadSchedules();
	}

	async function deleteProjectSchedule(sc) {
		if (!confirm(`Delete schedule "${sc.name}"?`)) return;
		await apiDelete(`/api/projects/${encodeURIComponent(projectName)}/schedules/${sc.id}`);
		await loadSchedules();
	}

	async function runScheduleNow(sc) {
		await apiPost(`/api/projects/${encodeURIComponent(projectName)}/schedules/${sc.id}/run`, {});
		await loadSchedules();
	}

	function formatScheduleExpr(sc) {
		if (sc.schedule_type === 'cron') return sc.cron_expr;
		const secs = sc.interval_seconds;
		if (secs >= 86400 && secs % 86400 === 0) return `Every ${secs / 86400}d`;
		if (secs >= 3600 && secs % 3600 === 0) return `Every ${secs / 3600}h`;
		return `Every ${secs / 60}m`;
	}

	// Skill functions
	async function loadProjectSkills() {
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/skills`);
		if (res?.data) {
			projectSkills = res.data;
		}
		skillsLoaded = true;
	}

	function openSkillCreate() {
		editingSkillItem = null;
		showSkillForm = true;
	}

	function openSkillEdit(sk) {
		editingSkillItem = sk;
		showSkillForm = true;
	}

	function closeSkillForm() {
		showSkillForm = false;
		editingSkillItem = null;
	}

	async function handleSkillSave(data) {
		if (editingSkillItem) {
			const res = await apiPatch(`/api/skills/${editingSkillItem.id}`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to update');
		} else {
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/skills`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to create');
		}
		closeSkillForm();
		await loadProjectSkills();
	}

	async function toggleSkill(sk) {
		await apiPatch(`/api/skills/${sk.id}`, { enabled: !sk.enabled });
		await loadProjectSkills();
	}

	async function deleteProjectSkill(sk) {
		if (!confirm(`Delete skill "${sk.name}"?`)) return;
		await apiDelete(`/api/skills/${sk.id}`);
		await loadProjectSkills();
	}
</script>

{#if loading}
	<div class="project-page">
		<p class="text-muted">Loading...</p>
	</div>
{:else if !project}
	<div class="project-page">
		<div class="card empty-state">
			<p>Project "{projectName}" not found.</p>
			<a href="/projects" class="btn btn-primary">Back to Projects</a>
		</div>
	</div>
{:else}
	<div class="project-page">
		<!-- Compact header: name + path inline -->
		<div class="project-header-bar">
			<div class="header-title-row">
				<a href="/projects" class="back-link" title="Back to Projects">&larr;</a>
				<h1 class="project-title">{project.name}</h1>
				<span class="header-path mono">{project.path}</span>
				{#if project.repo_url}
					<a href={project.repo_url} target="_blank" rel="noopener noreferrer" class="github-link" title="Open on GitHub">
						<svg class="github-icon" viewBox="0 0 16 16" fill="currentColor" width="16" height="16">
							<path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
						</svg>
					</a>
				{/if}
				{#if project.description}
					<span class="header-desc">— {project.description}</span>
				{/if}
			</div>
			<div class="header-status">
				{#if sheepName}
					<select class="provider-select" value={sheepProvider}
						onchange={(e) => changeProvider(e.target.value)}>
						<option value="claude">Claude</option>
						<option value="opencode">OpenCode</option>
						<option value="auto">Auto</option>
					</select>
					<span class="sheep-label">{sheepName}</span>
					<StatusBadge status={sheepStatus} />
				{:else}
					<span class="no-sheep">No sheep</span>
				{/if}
			</div>
		</div>

		<!-- Tabs -->
		<div class="tabs">
			<button class="tab" class:active={activeTab === 'output'}
				onclick={() => switchTab('output')}>
				Live Output
			</button>
			<button class="tab" class:active={activeTab === 'history'}
				onclick={() => switchTab('history')}>
				Task History
			</button>
			<button class="tab" class:active={activeTab === 'files'}
				onclick={() => switchTab('files')}>
				Files
			</button>
			<button class="tab" class:active={activeTab === 'git'}
				onclick={() => switchTab('git')}>
				Git
			</button>
			<button class="tab" class:active={activeTab === 'schedules'}
				onclick={() => switchTab('schedules')}>
				Schedules
			</button>
			<button class="tab" class:active={activeTab === 'skills'}
				onclick={() => switchTab('skills')}>
				Skills
			</button>
			<button class="tab" class:active={activeTab === 'specs'}
				onclick={() => switchTab('specs')}>
				Specs
			</button>
		</div>

		<!-- Content area -->
		<div class="content-area" class:has-input={activeTab === 'output'}>
			<!-- Live Output tab -->
			{#if activeTab === 'output'}
				<div class="output-fill">
					<OutputViewer lines={liveOutput} maxHeight="none" />
				</div>
			{/if}

			<!-- Task History tab -->
			{#if activeTab === 'history'}
				<div class="history-fill">
					<div class="task-search">
						<input
							class="input task-search-input"
							type="text"
							placeholder="Search tasks..."
							bind:value={taskSearchQuery}
							onkeydown={onTaskSearch}
						/>
					</div>
					{#if !tasksLoaded}
						<p class="text-muted">Loading tasks...</p>
					{:else if tasks.length === 0}
						<p class="text-muted">{taskSearchQuery ? 'No matching tasks' : 'No tasks yet'}</p>
					{:else}
						<div class="task-list">
							{#each tasks as t (t.id)}
								<a href="/tasks/{t.id}" class="card task-history-item">
									<div class="task-row">
										<span class="task-id mono">#{t.id}</span>
										<StatusBadge status={t.status} />
										<span class="task-prompt-text">{truncate(t.prompt, 80)}</span>
										<span class="task-time mono">{formatTime(t.created_at)}</span>
									</div>
									{#if t.summary}
										<div class="task-summary-text">{truncate(t.summary, 120)}</div>
									{/if}
								</a>
							{/each}
						</div>
						{#if taskTotalPages > 1}
							<Pagination page={taskPage} totalPages={taskTotalPages}
								total={taskTotal} limit={taskLimit} onChange={onTaskPageChange} />
						{/if}
					{/if}
				</div>
			{/if}

			<!-- Files tab -->
			{#if activeTab === 'files'}
				<div class="files-fill">
					{#key filesKey}
						<FileBrowser {projectName} />
					{/key}
				</div>
			{/if}

			<!-- Git tab -->
			{#if activeTab === 'git'}
				<div class="git-fill">
					<GitGraph {projectName} />
				</div>
			{/if}

			<!-- Schedules tab -->
			{#if activeTab === 'schedules'}
				<div class="schedules-fill">
					<div class="schedules-header">
						<button class="btn btn-primary" onclick={openScheduleCreate}>+ New Schedule</button>
					</div>

					{#if !schedulesLoaded}
						<p class="text-muted">Loading schedules...</p>
					{:else if projectSchedules.length === 0 && !showScheduleForm}
						<p class="text-muted">No schedules for this project.</p>
					{:else}
						<div class="schedule-list-compact">
							{#each projectSchedules as sc (sc.id)}
								<div class="card schedule-card" class:disabled-card={!sc.enabled}>
									<div class="sc-row">
										<span class="sc-name">{sc.name}</span>
										<span class="sc-badge" class:cron-badge={sc.schedule_type === 'cron'}>{sc.schedule_type}</span>
										<span class="sc-expr mono">{formatScheduleExpr(sc)}</span>
										<div class="sc-actions">
											<button class="btn btn-xs" onclick={() => runScheduleNow(sc)}>Run</button>
											<button class="btn btn-xs" onclick={() => toggleSchedule(sc)}>{sc.enabled ? 'Off' : 'On'}</button>
											<button class="btn btn-xs" onclick={() => openScheduleEdit(sc)}>Edit</button>
											<button class="btn btn-xs btn-danger-xs" onclick={() => deleteProjectSchedule(sc)}>Del</button>
										</div>
									</div>
									<div class="sc-prompt">{truncate(sc.prompt, 100)}</div>
									<div class="sc-meta">
										{#if sc.next_run}<span>Next: {sc.next_run}</span>{/if}
										{#if sc.last_run}<span>Last: {sc.last_run}</span>{/if}
									</div>
								</div>
							{/each}
						</div>
					{/if}

					{#if showScheduleForm}
						<div class="modal-overlay" onclick={closeScheduleForm}>
							<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
								<div class="modal-hdr">
									<h2>{editingSchedule ? 'Edit Schedule' : 'New Schedule'}</h2>
									<button class="btn" onclick={closeScheduleForm}>Close</button>
								</div>
								<ScheduleForm
									schedule={editingSchedule}
									fixedProject={projectName}
									onSave={handleScheduleSave}
									onCancel={closeScheduleForm}
								/>
							</div>
						</div>
					{/if}
				</div>
			{/if}

			<!-- Specs tab -->
			{#if activeTab === 'specs'}
				<div class="spec-fill">
					<SpecViewer {projectName} />
				</div>
			{/if}

			<!-- Skills tab -->
			{#if activeTab === 'skills'}
				<div class="schedules-fill">
					<div class="schedules-header">
						<button class="btn btn-primary" onclick={openSkillCreate}>+ New Skill</button>
					</div>

					{#if !skillsLoaded}
						<p class="text-muted">Loading skills...</p>
					{:else if projectSkills.length === 0 && !showSkillForm}
						<p class="text-muted">No skills for this project.</p>
					{:else}
						<div class="schedule-list-compact">
							{#each projectSkills as sk (sk.id)}
								<div class="card schedule-card" class:disabled-card={!sk.enabled}>
									<div class="sc-row">
										<span class="sc-name">{sk.name}</span>
										{#if sk.bundled}
											<span class="sc-badge" style="background:rgba(163,113,247,0.15);color:#a371f7">bundled</span>
										{/if}
										<div class="sc-actions">
											<button class="btn btn-xs" onclick={() => toggleSkill(sk)}>{sk.enabled ? 'Off' : 'On'}</button>
											<button class="btn btn-xs" onclick={() => openSkillEdit(sk)}>Edit</button>
											{#if !sk.bundled}
												<button class="btn btn-xs btn-danger-xs" onclick={() => deleteProjectSkill(sk)}>Del</button>
											{/if}
										</div>
									</div>
									{#if sk.description}
										<div class="sc-prompt">{sk.description}</div>
									{/if}
									<div class="sc-meta">
										{#if sk.tags?.length > 0}
											{#each sk.tags as tag}
												<span style="font-size:10px;padding:1px 6px;border-radius:8px;background:var(--bg-tertiary)">{tag}</span>
											{/each}
										{/if}
									</div>
								</div>
							{/each}
						</div>
					{/if}

					{#if showSkillForm}
						<div class="modal-overlay" onclick={closeSkillForm}>
							<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
								<div class="modal-hdr">
									<h2>{editingSkillItem ? 'Edit Skill' : 'New Skill'}</h2>
									<button class="btn" onclick={closeSkillForm}>Close</button>
								</div>
								<SkillForm
									skill={editingSkillItem}
									fixedProject={projectName}
									fixedScope="project"
									onSave={handleSkillSave}
									onCancel={closeSkillForm}
								/>
							</div>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- Command input: only on Live Output tab -->
		{#if activeTab === 'output'}
			<div class="command-bar card">
				{#if sheepName}
					<CommandInput projectName={project.name} sheepName={sheepName} sheepStatus={sheepStatus} />
				{:else}
					<p class="text-muted command-disabled-text">Assign a sheep to this project to send tasks</p>
				{/if}
			</div>
		{/if}
	</div>
{/if}

<style>
	.project-page {
		display: flex;
		flex-direction: column;
		height: calc(100vh - 48px);
		overflow: hidden;
	}

	.text-muted { color: var(--text-secondary); }

	/* Compact header */
	.project-header-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 0;
		flex-shrink: 0;
		gap: 12px;
		min-height: 0;
	}

	.header-title-row {
		display: flex;
		align-items: baseline;
		gap: 8px;
		min-width: 0;
		flex: 1;
		overflow: hidden;
	}

	.back-link {
		color: var(--text-secondary);
		text-decoration: none;
		font-size: 16px;
		flex-shrink: 0;
	}

	.back-link:hover {
		color: var(--accent);
	}

	.project-title {
		font-size: 18px;
		font-weight: 600;
		margin: 0;
		flex-shrink: 0;
	}

	.header-path {
		font-size: 12px;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	.header-desc {
		font-size: 12px;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.github-link {
		flex-shrink: 0;
		color: var(--text-secondary);
		display: flex;
		align-items: center;
		transition: color 0.15s;
	}

	.github-link:hover {
		color: var(--text-primary);
	}

	.github-icon {
		width: 16px;
		height: 16px;
	}

	.header-status {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.provider-select {
		padding: 2px 6px;
		font-size: 12px;
		border-radius: var(--radius);
		background: var(--bg-tertiary);
		color: var(--text-primary);
		border: 1px solid var(--border);
		cursor: pointer;
	}

	.provider-select:hover {
		border-color: var(--accent);
	}

	.sheep-label {
		font-weight: 500;
		font-size: 13px;
		color: var(--accent);
	}

	.no-sheep {
		font-size: 12px;
		color: var(--text-secondary);
	}

	/* Tabs */
	.tabs {
		display: flex;
		gap: 0;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
		overflow-x: auto;
		-webkit-overflow-scrolling: touch;
	}

	.tab {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 13px;
		padding: 6px 14px;
		cursor: pointer;
		border-bottom: 2px solid transparent;
		transition: color 0.15s, border-color 0.15s;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.tab:hover {
		color: var(--text-primary);
	}

	.tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
	}

	/* Content area: fills remaining space */
	.content-area {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.output-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.output-fill :global(.output-viewer) {
		flex: 1;
		max-height: none !important;
		min-height: 0;
	}

	.task-search {
		flex-shrink: 0;
		padding: 8px 0;
	}

	.task-search-input {
		width: 100%;
		font-size: 13px;
	}

	.history-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 8px 0;
	}

	/* Task list */
	.task-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.task-history-item {
		text-decoration: none;
		color: inherit;
		transition: border-color 0.15s;
		padding: 10px 14px;
	}

	.task-history-item:hover {
		border-color: var(--accent);
		text-decoration: none;
	}

	.task-row {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
	}

	.task-id {
		font-size: 12px;
		color: var(--accent);
		font-weight: 600;
		flex-shrink: 0;
	}

	.task-prompt-text {
		flex: 1;
		font-size: 13px;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-time {
		font-size: 11px;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.task-summary-text {
		font-size: 12px;
		color: var(--text-secondary);
		margin-top: 4px;
		line-height: 1.4;
	}

	/* Command bar: fixed at bottom */
	.command-bar {
		flex-shrink: 0;
		margin-top: 0;
		border-top: 1px solid var(--border);
	}

	.command-disabled-text {
		text-align: center;
		margin: 0;
		font-size: 13px;
	}

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	/* Git tab */
	.git-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	/* Specs tab */
	.spec-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	/* Files tab */
	.files-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	@media (max-width: 768px) {
		.project-page {
			height: calc(100dvh - 12px);
			margin-top: -44px;
			overflow-x: hidden;
		}

		.project-header-bar {
			flex-wrap: wrap;
			gap: 4px;
			padding: 4px 0 4px 56px;
			box-sizing: border-box;
			width: 100%;
		}

		.header-title-row {
			align-items: center;
			flex-wrap: nowrap;
			gap: 6px;
			min-width: 0;
		}

		.project-title {
			font-size: 16px;
		}

		.header-path {
			display: none;
		}

		.header-desc {
			display: none;
		}

		.header-status {
			width: 100%;
		}

		.provider-select {
			padding: 6px 10px;
			font-size: 13px;
		}

		.tab {
			padding: 8px 10px;
			font-size: 12px;
		}

		.task-history-item {
			padding: 8px 10px;
		}
	}

	/* Schedules tab */
	.schedules-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 8px 0;
	}

	.schedules-header {
		display: flex;
		justify-content: flex-end;
		margin-bottom: 12px;
	}

	.btn-primary {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.schedule-list-compact {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.schedule-card {
		transition: border-color 0.15s;
	}

	.schedule-card:hover {
		border-color: var(--accent);
	}

	.disabled-card {
		opacity: 0.5;
	}

	.sc-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 4px;
		flex-wrap: wrap;
	}

	.sc-name {
		font-weight: 600;
		font-size: 13px;
	}

	.sc-badge {
		font-size: 10px;
		padding: 1px 5px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
		font-weight: 600;
	}

	.cron-badge {
		background: rgba(56, 139, 253, 0.15);
		color: var(--accent);
	}

	.sc-expr {
		font-size: 11px;
		color: var(--text-secondary);
	}

	.sc-actions {
		margin-left: auto;
		display: flex;
		gap: 3px;
	}

	.btn-xs {
		padding: 2px 6px;
		font-size: 10px;
	}

	.btn-danger-xs {
		color: var(--danger);
		border-color: var(--danger);
	}

	.sc-prompt {
		font-size: 12px;
		color: var(--text-primary);
		margin-bottom: 4px;
	}

	.sc-meta {
		display: flex;
		gap: 10px;
		font-size: 11px;
		color: var(--text-secondary);
	}

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
		max-width: 600px;
		max-height: 85vh;
		overflow-y: auto;
		padding: 24px;
	}

	.modal-hdr {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 16px;
	}

	.modal-hdr h2 {
		font-size: 16px;
	}
</style>
