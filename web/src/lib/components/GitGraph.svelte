<script>
	import { apiGet } from '$lib/api.js';

	let { projectName = '' } = $props();

	// Sub-tab state
	let subTab = $state('commits');

	// Commits
	let commits = $state([]);
	let commitsTotal = $state(0);
	let commitsLoading = $state(false);
	let commitsLoaded = $state(false);
	let selectedCommit = $state(null);
	let commitDetail = $state(null);
	let detailLoading = $state(false);

	// Branches
	let branches = $state([]);
	let branchesLoading = $state(false);
	let branchesLoaded = $state(false);

	// Changes
	let changes = $state([]);
	let changesLoading = $state(false);
	let changesLoaded = $state(false);

	// Diff viewer
	let diffData = $state(null);
	let diffLoading = $state(false);
	let diffFile = $state('');

	// Graph computation
	let graphData = $state([]);

	// Colors for graph lanes
	const LANE_COLORS = [
		'#4fc3f7', '#81c784', '#ffb74d', '#e57373',
		'#ba68c8', '#4dd0e1', '#fff176', '#f06292',
		'#aed581', '#90a4ae',
	];

	const ROW_HEIGHT = 32;
	const LANE_WIDTH = 16;
	const DOT_RADIUS = 4;

	$effect(() => {
		if (projectName) {
			loadCommits();
		}
	});

	function switchSubTab(tab) {
		subTab = tab;
		if (tab === 'commits' && !commitsLoaded) loadCommits();
		if (tab === 'branches' && !branchesLoaded) loadBranches();
		if (tab === 'changes' && !changesLoaded) loadChanges();
	}

	async function loadCommits() {
		commitsLoading = true;
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/git/log?limit=200`);
		if (res?.data) {
			commits = res.data.commits || [];
			commitsTotal = res.data.total || 0;
			graphData = computeGraph(commits);
		}
		commitsLoaded = true;
		commitsLoading = false;
	}

	async function loadBranches() {
		branchesLoading = true;
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/git/branches`);
		if (res?.data) {
			branches = res.data;
		}
		branchesLoaded = true;
		branchesLoading = false;
	}

	async function loadChanges() {
		changesLoading = true;
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/git/changes`);
		if (res?.data) {
			changes = res.data;
		}
		changesLoaded = true;
		changesLoading = false;
	}

	async function selectCommit(commit) {
		if (selectedCommit?.hash === commit.hash) {
			selectedCommit = null;
			commitDetail = null;
			diffData = null;
			diffFile = '';
			return;
		}
		selectedCommit = commit;
		detailLoading = true;
		diffData = null;
		diffFile = '';
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/git/commits/${commit.hash}`);
		if (res?.data) {
			commitDetail = res.data;
		}
		detailLoading = false;
	}

	async function loadFileDiff(filePath) {
		if (diffFile === filePath) {
			// Toggle off
			diffData = null;
			diffFile = '';
			return;
		}
		diffFile = filePath;
		diffLoading = true;
		const res = await apiGet(
			`/api/projects/${encodeURIComponent(projectName)}/git/commits/${selectedCommit.hash}/diff?file=${encodeURIComponent(filePath)}`
		);
		if (res?.data) {
			diffData = res.data;
		}
		diffLoading = false;
	}

	// Lane assignment algorithm for git graph
	function computeGraph(commits) {
		if (!commits.length) return [];

		const rows = [];
		// activeLanes: array of commit hashes that are "active" in each lane
		let activeLanes = [];

		for (let i = 0; i < commits.length; i++) {
			const commit = commits[i];
			const hash = commit.hash;
			const parents = commit.parents || [];

			// Find which lane this commit is in
			let lane = activeLanes.indexOf(hash);
			if (lane === -1) {
				// New commit not in any lane - assign to first empty or append
				lane = activeLanes.indexOf(null);
				if (lane === -1) {
					lane = activeLanes.length;
					activeLanes.push(hash);
				} else {
					activeLanes[lane] = hash;
				}
			}

			// Build connections: which lanes continue through this row
			const connections = [];

			// First, mark lanes that pass through (not this commit's lane)
			for (let l = 0; l < activeLanes.length; l++) {
				if (l !== lane && activeLanes[l] !== null && activeLanes[l] !== hash) {
					connections.push({ from: l, to: l, type: 'pass' });
				}
			}

			// Remove current commit from its lane
			activeLanes[lane] = null;

			// Place parents
			if (parents.length === 0) {
				// Root commit - lane ends
			} else if (parents.length === 1) {
				// Single parent
				const parentHash = parents[0];
				const existingLane = activeLanes.indexOf(parentHash);
				if (existingLane !== -1) {
					// Parent already in a lane - merge into it
					connections.push({ from: lane, to: existingLane, type: 'merge' });
				} else {
					// Put parent in same lane
					activeLanes[lane] = parentHash;
					connections.push({ from: lane, to: lane, type: 'direct' });
				}
			} else {
				// Merge commit - multiple parents
				for (let pi = 0; pi < parents.length; pi++) {
					const parentHash = parents[pi];
					const existingLane = activeLanes.indexOf(parentHash);
					if (existingLane !== -1) {
						connections.push({ from: lane, to: existingLane, type: 'merge' });
					} else if (pi === 0) {
						// First parent goes in same lane
						activeLanes[lane] = parentHash;
						connections.push({ from: lane, to: lane, type: 'direct' });
					} else {
						// Other parents get new lanes
						let newLane = activeLanes.indexOf(null);
						if (newLane === -1) {
							newLane = activeLanes.length;
							activeLanes.push(parentHash);
						} else {
							activeLanes[newLane] = parentHash;
						}
						connections.push({ from: lane, to: newLane, type: 'branch' });
					}
				}
			}

			// Trim trailing nulls
			while (activeLanes.length > 0 && activeLanes[activeLanes.length - 1] === null) {
				activeLanes.pop();
			}

			rows.push({
				lane,
				connections,
				maxLane: Math.max(activeLanes.length, lane + 1),
				commit,
			});
		}

		return rows;
	}

	function formatDate(dateStr) {
		const d = new Date(dateStr);
		const now = new Date();
		const diff = now - d;
		const mins = Math.floor(diff / 60000);
		const hours = Math.floor(diff / 3600000);
		const days = Math.floor(diff / 86400000);

		if (mins < 1) return 'just now';
		if (mins < 60) return `${mins}m ago`;
		if (hours < 24) return `${hours}h ago`;
		if (days < 30) return `${days}d ago`;

		return d.toLocaleDateString('ko-KR', { year: 'numeric', month: '2-digit', day: '2-digit' });
	}

	function statusLabel(s) {
		const map = { 'M': 'Modified', 'A': 'Added', 'D': 'Deleted', '??': 'Untracked', 'R': 'Renamed', 'C': 'Copied' };
		return map[s] || s;
	}

	function statusClass(s) {
		if (s === 'M' || s === 'MM') return 'modified';
		if (s === 'A' || s === 'AM') return 'added';
		if (s === 'D') return 'deleted';
		if (s === '??') return 'untracked';
		return 'modified';
	}

	// Compute SVG graph width from rows
	function graphWidth(rows) {
		let max = 1;
		for (const r of rows) {
			if (r.maxLane > max) max = r.maxLane;
			if (r.lane + 1 > max) max = r.lane + 1;
		}
		return (max + 1) * LANE_WIDTH;
	}
</script>

<div class="git-container">
	<!-- Sub-tabs -->
	<div class="sub-tabs">
		<button class="sub-tab" class:active={subTab === 'commits'}
			onclick={() => switchSubTab('commits')}>Commits</button>
		<button class="sub-tab" class:active={subTab === 'changes'}
			onclick={() => switchSubTab('changes')}>Changes</button>
		<button class="sub-tab" class:active={subTab === 'branches'}
			onclick={() => switchSubTab('branches')}>Branches</button>
		{#if subTab === 'commits' && commitsTotal > 0}
			<span class="commit-count mono">{commitsTotal} commits</span>
		{/if}
	</div>

	<!-- Commits sub-tab -->
	{#if subTab === 'commits'}
		<div class="commits-panel">
			{#if commitsLoading && !commitsLoaded}
				<p class="text-muted">Loading commits...</p>
			{:else if commits.length === 0}
				<p class="text-muted">No commits found</p>
			{:else}
				{@const gw = graphWidth(graphData)}
				<div class="commit-list-scroll">
					<div class="commit-list" style="position:relative;">
						{#each graphData as row, i}
							<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
							<div class="commit-row" class:selected={selectedCommit?.hash === row.commit.hash}
								onclick={() => selectCommit(row.commit)}
								style="height:{ROW_HEIGHT}px;">
								<!-- Graph SVG -->
								<svg class="graph-svg" width={gw} height={ROW_HEIGHT}
									style="min-width:{gw}px;">
									<!-- Connections -->
									{#each row.connections as conn}
										{#if conn.type === 'pass'}
											<line
												x1={conn.from * LANE_WIDTH + LANE_WIDTH / 2}
												y1="0"
												x2={conn.to * LANE_WIDTH + LANE_WIDTH / 2}
												y2={ROW_HEIGHT}
												stroke={LANE_COLORS[conn.to % LANE_COLORS.length]}
												stroke-width="2"
												stroke-opacity="0.6"
											/>
										{:else if conn.type === 'direct'}
											<line
												x1={conn.from * LANE_WIDTH + LANE_WIDTH / 2}
												y1={ROW_HEIGHT / 2}
												x2={conn.to * LANE_WIDTH + LANE_WIDTH / 2}
												y2={ROW_HEIGHT}
												stroke={LANE_COLORS[conn.to % LANE_COLORS.length]}
												stroke-width="2"
												stroke-opacity="0.6"
											/>
										{:else if conn.type === 'merge' || conn.type === 'branch'}
											<path
												d="M {conn.from * LANE_WIDTH + LANE_WIDTH / 2} {ROW_HEIGHT / 2}
												   C {conn.from * LANE_WIDTH + LANE_WIDTH / 2} {ROW_HEIGHT * 0.8},
												     {conn.to * LANE_WIDTH + LANE_WIDTH / 2} {ROW_HEIGHT * 0.7},
												     {conn.to * LANE_WIDTH + LANE_WIDTH / 2} {ROW_HEIGHT}"
												fill="none"
												stroke={LANE_COLORS[conn.to % LANE_COLORS.length]}
												stroke-width="2"
												stroke-opacity="0.6"
											/>
										{/if}
									{/each}

									<!-- Vertical line from top to dot (if previous row connects) -->
									{#if i > 0}
										{@const prevRow = graphData[i - 1]}
										{#if prevRow.connections.some(c => (c.type === 'direct' || c.type === 'merge' || c.type === 'branch') && c.to === row.lane)}
											<line
												x1={row.lane * LANE_WIDTH + LANE_WIDTH / 2}
												y1="0"
												x2={row.lane * LANE_WIDTH + LANE_WIDTH / 2}
												y2={ROW_HEIGHT / 2}
												stroke={LANE_COLORS[row.lane % LANE_COLORS.length]}
												stroke-width="2"
												stroke-opacity="0.6"
											/>
										{/if}
									{/if}

									<!-- Commit dot -->
									<circle
										cx={row.lane * LANE_WIDTH + LANE_WIDTH / 2}
										cy={ROW_HEIGHT / 2}
										r={DOT_RADIUS}
										fill={LANE_COLORS[row.lane % LANE_COLORS.length]}
									/>
								</svg>

								<!-- Commit info -->
								<div class="commit-info">
									<span class="commit-hash mono">{row.commit.short_hash}</span>
									{#if row.commit.refs?.length}
										<span class="commit-refs">
											{#each row.commit.refs as ref}
												<span class="ref-badge" class:head={ref.includes('HEAD')}
													class:remote={ref.startsWith('origin/')}>{ref}</span>
											{/each}
										</span>
									{/if}
									<span class="commit-subject">{row.commit.subject}</span>
								</div>
								<div class="commit-meta">
									<span class="commit-author">{row.commit.author}</span>
									<span class="commit-date mono">{formatDate(row.commit.date)}</span>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			<!-- Detail panel -->
			{#if selectedCommit}
				<div class="detail-panel">
					<div class="detail-header">
						<span class="detail-title">Commit Detail</span>
						<button class="detail-close" onclick={() => { selectedCommit = null; commitDetail = null; diffData = null; diffFile = ''; }}>x</button>
					</div>
					{#if detailLoading}
						<p class="text-muted detail-loading">Loading...</p>
					{:else if commitDetail}
						<div class="detail-body">
							<div class="detail-info-row">
								<span class="detail-hash mono">{commitDetail.short_hash}</span>
								<span class="detail-author">{commitDetail.author} &lt;{commitDetail.email}&gt;</span>
								<span class="detail-date mono">{new Date(commitDetail.date).toLocaleString('ko-KR')}</span>
							</div>
							{#if commitDetail.refs?.length}
								<div class="detail-refs">
									{#each commitDetail.refs as ref}
										<span class="ref-badge" class:head={ref.includes('HEAD')}
											class:remote={ref.startsWith('origin/')}>{ref}</span>
									{/each}
								</div>
							{/if}
							<div class="detail-message">{commitDetail.body}</div>
							{#if commitDetail.files?.length}
								<div class="detail-files">
									<span class="files-header">Files ({commitDetail.files.length})</span>
									{#each commitDetail.files as f}
										<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
										<div class="file-row clickable" class:file-active={diffFile === f.path}
											onclick={() => loadFileDiff(f.path)}>
											<span class="file-path mono">{f.path}</span>
											<span class="file-stat">
												{#if f.additions > 0}<span class="stat-add">+{f.additions}</span>{/if}
												{#if f.deletions > 0}<span class="stat-del">-{f.deletions}</span>{/if}
											</span>
										</div>
										<!-- Inline diff below the selected file -->
										{#if diffFile === f.path}
											<div class="diff-container">
												{#if diffLoading}
													<p class="text-muted diff-loading">Loading diff...</p>
												{:else if diffData}
													{#if diffData.is_binary}
														<p class="text-muted diff-binary">Binary file</p>
													{:else if diffData.hunks.length === 0}
														<p class="text-muted diff-empty">No diff available</p>
													{:else}
														{#each diffData.hunks as hunk}
															<div class="diff-hunk">
																<div class="diff-hunk-header mono">{hunk.header}</div>
																{#each hunk.lines as line}
																	<div class="diff-line diff-{line.type}">
																		<span class="diff-ln old mono">{line.old_line || ''}</span>
																		<span class="diff-ln new mono">{line.new_line || ''}</span>
																		<span class="diff-prefix mono">{line.type === 'add' ? '+' : line.type === 'delete' ? '-' : ' '}</span>
																		<span class="diff-code">{line.content}</span>
																	</div>
																{/each}
															</div>
														{/each}
													{/if}
												{/if}
											</div>
										{/if}
									{/each}
								</div>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>
	{/if}

	<!-- Changes sub-tab -->
	{#if subTab === 'changes'}
		<div class="changes-panel">
			{#if changesLoading && !changesLoaded}
				<p class="text-muted">Loading changes...</p>
			{:else if changes.length === 0}
				<p class="text-muted">Working directory clean</p>
			{:else}
				<div class="changes-list">
					{#each changes as ch}
						<div class="change-row">
							<span class="change-status {statusClass(ch.status)}">{ch.status}</span>
							<span class="change-path mono">{ch.path}</span>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	{/if}

	<!-- Branches sub-tab -->
	{#if subTab === 'branches'}
		<div class="branches-panel">
			{#if branchesLoading && !branchesLoaded}
				<p class="text-muted">Loading branches...</p>
			{:else if branches.length === 0}
				<p class="text-muted">No branches found</p>
			{:else}
				{@const localBranches = branches.filter(b => !b.is_remote)}
				{@const remoteBranches = branches.filter(b => b.is_remote)}
				<div class="branch-sections">
					{#if localBranches.length > 0}
						<div class="branch-section">
							<span class="section-label">Local</span>
							{#each localBranches as b}
								<div class="branch-row" class:current={b.is_current}>
									{#if b.is_current}<span class="current-marker">*</span>{/if}
									<span class="branch-name mono">{b.name}</span>
									<span class="branch-head mono">{b.head}</span>
								</div>
							{/each}
						</div>
					{/if}
					{#if remoteBranches.length > 0}
						<div class="branch-section">
							<span class="section-label">Remote</span>
							{#each remoteBranches as b}
								<div class="branch-row">
									<span class="branch-name mono">{b.name}</span>
									<span class="branch-head mono">{b.head}</span>
								</div>
							{/each}
						</div>
					{/if}
				</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.git-container {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
	}

	.text-muted {
		color: var(--text-secondary);
		padding: 16px;
		font-size: 13px;
	}

	.mono {
		font-family: var(--font-mono), monospace;
	}

	/* Sub-tabs */
	.sub-tabs {
		display: flex;
		align-items: center;
		gap: 0;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
		padding: 0 4px;
	}

	.sub-tab {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 12px;
		padding: 6px 12px;
		cursor: pointer;
		border-bottom: 2px solid transparent;
		transition: color 0.15s, border-color 0.15s;
	}

	.sub-tab:hover {
		color: var(--text-primary);
	}

	.sub-tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
	}

	.commit-count {
		margin-left: auto;
		font-size: 11px;
		color: var(--text-secondary);
		padding-right: 8px;
	}

	/* Commits panel */
	.commits-panel {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.commit-list-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.commit-list {
		display: flex;
		flex-direction: column;
	}

	.commit-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 0 8px;
		cursor: pointer;
		border-bottom: 1px solid transparent;
		transition: background 0.1s;
	}

	.commit-row:hover {
		background: var(--bg-tertiary);
	}

	.commit-row.selected {
		background: var(--bg-tertiary);
		border-bottom-color: var(--border);
	}

	.graph-svg {
		flex-shrink: 0;
	}

	.commit-info {
		display: flex;
		align-items: center;
		gap: 6px;
		flex: 1;
		min-width: 0;
		overflow: hidden;
	}

	.commit-hash {
		font-size: 12px;
		color: var(--accent);
		flex-shrink: 0;
	}

	.commit-refs {
		display: flex;
		gap: 4px;
		flex-shrink: 0;
	}

	.ref-badge {
		font-size: 10px;
		padding: 1px 6px;
		border-radius: 3px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		border: 1px solid var(--border);
		white-space: nowrap;
	}

	.ref-badge.head {
		background: #2d4a22;
		color: #81c784;
		border-color: #4caf50;
	}

	.ref-badge.remote {
		background: #1a3a4a;
		color: #4fc3f7;
		border-color: #29b6f6;
	}

	.commit-subject {
		font-size: 13px;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	.commit-meta {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-shrink: 0;
		padding-right: 4px;
	}

	.commit-author {
		font-size: 12px;
		color: var(--text-secondary);
		white-space: nowrap;
	}

	.commit-date {
		font-size: 11px;
		color: var(--text-secondary);
		white-space: nowrap;
	}

	/* Detail panel */
	.detail-panel {
		flex-shrink: 0;
		border-top: 1px solid var(--border);
		max-height: 40%;
		overflow-y: auto;
		background: var(--bg-secondary);
	}

	.detail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 6px 12px;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
	}

	.detail-title {
		font-size: 12px;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.detail-close {
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		font-size: 14px;
		padding: 0 4px;
		line-height: 1;
	}

	.detail-close:hover {
		color: var(--text-primary);
	}

	.detail-loading {
		padding: 12px;
	}

	.detail-body {
		padding: 10px 12px;
	}

	.detail-info-row {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-wrap: wrap;
		margin-bottom: 6px;
	}

	.detail-hash {
		font-size: 13px;
		color: var(--accent);
		font-weight: 600;
	}

	.detail-author {
		font-size: 12px;
		color: var(--text-secondary);
	}

	.detail-date {
		font-size: 11px;
		color: var(--text-secondary);
	}

	.detail-refs {
		display: flex;
		gap: 4px;
		margin-bottom: 6px;
	}

	.detail-message {
		font-size: 13px;
		color: var(--text-primary);
		line-height: 1.5;
		padding: 8px 0;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.detail-files {
		border-top: 1px solid var(--border);
		padding-top: 8px;
	}

	.files-header {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-secondary);
		margin-bottom: 4px;
		display: block;
	}

	.file-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 2px 0;
		font-size: 12px;
	}

	.file-row.clickable {
		cursor: pointer;
		padding: 3px 4px;
		border-radius: 3px;
		transition: background 0.1s;
	}

	.file-row.clickable:hover {
		background: var(--bg-tertiary);
	}

	.file-row.file-active {
		background: var(--bg-tertiary);
	}

	.file-path {
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
		flex: 1;
	}

	.file-stat {
		flex-shrink: 0;
		display: flex;
		gap: 6px;
		margin-left: 12px;
	}

	.stat-add {
		color: #81c784;
		font-size: 11px;
		font-family: var(--font-mono), monospace;
	}

	.stat-del {
		color: #e57373;
		font-size: 11px;
		font-family: var(--font-mono), monospace;
	}

	/* Changes panel */
	.changes-panel {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 4px 0;
	}

	.changes-list {
		display: flex;
		flex-direction: column;
	}

	.change-row {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 4px 12px;
		font-size: 13px;
	}

	.change-row:hover {
		background: var(--bg-tertiary);
	}

	.change-status {
		font-size: 11px;
		font-weight: 700;
		font-family: var(--font-mono), monospace;
		width: 24px;
		text-align: center;
		flex-shrink: 0;
	}

	.change-status.modified { color: #ffb74d; }
	.change-status.added { color: #81c784; }
	.change-status.deleted { color: #e57373; }
	.change-status.untracked { color: var(--text-secondary); }

	.change-path {
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	/* Branches panel */
	.branches-panel {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 8px 0;
	}

	.branch-sections {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.branch-section {
		display: flex;
		flex-direction: column;
	}

	.section-label {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-secondary);
		text-transform: uppercase;
		padding: 4px 12px;
		letter-spacing: 0.5px;
	}

	.branch-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 12px;
		font-size: 13px;
	}

	.branch-row:hover {
		background: var(--bg-tertiary);
	}

	.branch-row.current {
		background: var(--bg-tertiary);
	}

	.current-marker {
		color: #81c784;
		font-weight: 700;
		flex-shrink: 0;
		width: 10px;
	}

	.branch-name {
		color: var(--text-primary);
	}

	.branch-head {
		font-size: 11px;
		color: var(--text-secondary);
		margin-left: auto;
	}

	/* Diff viewer */
	.diff-container {
		margin: 4px 0 8px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
		max-height: 400px;
		overflow-y: auto;
	}

	.diff-loading, .diff-binary, .diff-empty {
		padding: 8px 12px;
		font-size: 12px;
	}

	.diff-hunk {
		border-bottom: 1px solid var(--border);
	}

	.diff-hunk:last-child {
		border-bottom: none;
	}

	.diff-hunk-header {
		font-size: 11px;
		color: var(--text-secondary);
		background: var(--bg-tertiary);
		padding: 4px 12px;
		border-bottom: 1px solid var(--border);
	}

	.diff-line {
		display: flex;
		font-family: var(--font-mono), 'SF Mono', Monaco, 'Cascadia Code', monospace;
		font-size: 12px;
		line-height: 20px;
		white-space: pre;
	}

	.diff-line:hover {
		filter: brightness(1.1);
	}

	.diff-line.diff-add {
		background: rgba(63, 185, 80, 0.15);
	}

	.diff-line.diff-delete {
		background: rgba(248, 81, 73, 0.15);
	}

	.diff-line.diff-context {
		background: transparent;
	}

	.diff-ln {
		width: 40px;
		min-width: 40px;
		text-align: right;
		padding-right: 8px;
		color: var(--text-secondary);
		opacity: 0.5;
		user-select: none;
		flex-shrink: 0;
	}

	.diff-prefix {
		width: 20px;
		min-width: 20px;
		text-align: center;
		flex-shrink: 0;
		user-select: none;
	}

	.diff-add .diff-prefix {
		color: #81c784;
	}

	.diff-delete .diff-prefix {
		color: #e57373;
	}

	.diff-code {
		flex: 1;
		min-width: 0;
		overflow-x: auto;
		padding-right: 12px;
	}

	/* Mobile */
	@media (max-width: 768px) {
		.commit-meta {
			display: none;
		}

		.commit-info {
			gap: 4px;
		}

		.sub-tab {
			padding: 6px 8px;
			font-size: 11px;
		}
	}
</style>
