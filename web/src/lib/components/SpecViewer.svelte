<script>
	import { onMount, tick } from 'svelte';
	import { Marked } from 'marked';
	import html2canvas from 'html2canvas';
	import { jsPDF } from 'jspdf';
	import { apiGet } from '$lib/api.js';
	import { accessToken, wireframePreset, wireframeOptions } from '$lib/stores.js';
	import { get } from 'svelte/store';
	import { wireframePresets, allVarKeys } from '$lib/wireframe-presets.js';
	import '$lib/style/wireframe.css';

	let { projectName } = $props();

	let specs = $state([]);
	let loading = $state(true);
	let selectedSpec = $state(null);
	let specContent = $state('');
	let renderedHTML = $state('');
	let previewEl = $state(null);

	let mermaidLoaded = false;
	let mermaidCounter = 0;
	let pdfExporting = $state(false);

	let currentPreset = $state(get(wireframePreset));
	let compactMode = $state(get(wireframeOptions).compact || false);
	let showGrid = $state(get(wireframeOptions).showGrid || false);
	const presetEntries = Object.entries(wireframePresets);

	function applyPresetToContainers() {
		if (!previewEl) return;
		const containers = previewEl.querySelectorAll('.wf-container');
		const preset = wireframePresets[currentPreset] || wireframePresets.default;
		containers.forEach(el => {
			for (const key of allVarKeys) el.style.removeProperty(key);
			for (const [k, v] of Object.entries(preset.vars)) el.style.setProperty(k, v);
			el.classList.toggle('wf-compact', compactMode);
			el.classList.toggle('wf-show-grid', showGrid);
		});
	}

	function onPresetChange() {
		wireframePreset.set(currentPreset);
		applyPresetToContainers();
	}

	function onToggleChange() {
		wireframeOptions.set({ compact: compactMode, showGrid: showGrid });
		applyPresetToContainers();
	}

	const typeColors = {
		overview: '#3b82f6',
		api: '#22c55e',
		'db-schema': '#eab308',
		'db-erd': '#ec4899',
		screen: '#6366f1',
		flow: '#a855f7',
		requirements: '#14b8a6',
		infra: '#ef4444',
		env: '#6b7280'
	};

	function detectType(filename) {
		const name = filename.toLowerCase().replace(/\.md$/, '');
		for (const t of Object.keys(typeColors)) {
			if (name.startsWith(t)) return t;
		}
		return null;
	}

	function formatSize(bytes) {
		if (bytes < 1024) return bytes + ' B';
		return (bytes / 1024).toFixed(1) + ' KB';
	}

	async function loadSpecs() {
		loading = true;
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/specs`);
		if (res && res.data) {
			specs = res.data;
		}
		loading = false;
	}

	async function loadMermaid() {
		if (mermaidLoaded) return;
		await new Promise((resolve, reject) => {
			if (window.mermaid) {
				mermaidLoaded = true;
				resolve();
				return;
			}
			const script = document.createElement('script');
			script.src = 'https://cdnjs.cloudflare.com/ajax/libs/mermaid/11.4.0/mermaid.min.js';
			script.onload = () => {
				window.mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'loose' });
				mermaidLoaded = true;
				resolve();
			};
			script.onerror = reject;
			document.head.appendChild(script);
		});
	}

	function escapeHTML(str) {
		return str
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;')
			.replace(/"/g, '&quot;');
	}

	function sanitizeWireframeHTML(html) {
		html = html.replace(/<script[\s\S]*?<\/script>/gi, '');
		html = html.replace(/<iframe[\s\S]*?<\/iframe>/gi, '');
		html = html.replace(/<iframe[\s\S]*?\/?>/gi, '');
		html = html.replace(/<object[\s\S]*?<\/object>/gi, '');
		html = html.replace(/<embed[\s\S]*?\/?>/gi, '');
		html = html.replace(/<style[\s\S]*?<\/style>/gi, '');
		html = html.replace(/<link[\s\S]*?\/?>/gi, '');
		html = html.replace(/\son\w+\s*=\s*"[^"]*"/gi, '');
		html = html.replace(/\son\w+\s*=\s*'[^']*'/gi, '');
		html = html.replace(/\son\w+\s*=\s*[^\s>]*/gi, '');
		html = html.replace(/href\s*=\s*"javascript:[^"]*"/gi, 'href="#"');
		html = html.replace(/href\s*=\s*'javascript:[^']*'/gi, "href='#'");
		html = html.replace(/src\s*=\s*"javascript:[^"]*"/gi, '');
		html = html.replace(/src\s*=\s*'javascript:[^']*'/gi, '');
		return html;
	}

	async function openSpec(spec) {
		selectedSpec = spec;
		specContent = '';
		renderedHTML = '';

		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/specs/${spec.path}`);
		if (res && res.data) {
			specContent = res.data.content;
			await renderSpec(specContent);
		}
	}

	async function renderSpec(markdown) {
		await loadMermaid();

		const mermaidBlocks = [];
		const marked = new Marked();

		marked.use({
			renderer: {
				code(obj) {
					const code = typeof obj === 'object' ? obj.text : obj;
					const lang = typeof obj === 'object' ? obj.lang : '';

					if (lang === 'mermaid') {
						const id = 'mermaid-' + (++mermaidCounter);
						mermaidBlocks.push({ id, code });
						return '<div class="mermaid" id="' + id + '">' + escapeHTML(code) + '</div>';
					}
					if (lang === 'wireframe' || lang === 'html') {
						return '<div class="wf-container">' + sanitizeWireframeHTML(code) + '</div>';
					}
					return '<pre><code' + (lang ? ' class="language-' + lang + '"' : '') + '>' + escapeHTML(code) + '</code></pre>';
				}
			}
		});

		const html = marked.parse(markdown);
		renderedHTML = html;

		await tick();

		for (const block of mermaidBlocks) {
			const el = previewEl?.querySelector('#' + block.id);
			if (el) {
				try {
					const result = await window.mermaid.render(block.id + '-svg', block.code);
					el.innerHTML = result.svg;
				} catch (e) {
					el.innerHTML = '<pre style="color:#ef4444;font-size:13px">Mermaid Error: ' + escapeHTML(e.message || String(e)) + '</pre>';
				}
			}
		}

		applyPresetToContainers();
	}

	function goBack() {
		selectedSpec = null;
		specContent = '';
		renderedHTML = '';
	}

	function downloadSpec() {
		if (!selectedSpec) return;
		const token = get(accessToken);
		const url = `/api/projects/${encodeURIComponent(projectName)}/specs-download/${selectedSpec.path}?token=${encodeURIComponent(token)}`;
		const a = document.createElement('a');
		a.href = url;
		a.download = selectedSpec.name;
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
	}

	async function downloadPDF() {
		if (!previewEl || !selectedSpec || pdfExporting) return;
		pdfExporting = true;
		try {
			// Temporarily expand scroll containers so html2canvas captures full content
			const detail = previewEl.closest('.spec-detail');
			const savedStyles = [];
			for (const el of [previewEl, detail]) {
				if (!el) continue;
				savedStyles.push({ el, overflow: el.style.overflow, height: el.style.height, maxHeight: el.style.maxHeight });
				el.style.overflow = 'visible';
				el.style.height = 'auto';
				el.style.maxHeight = 'none';
			}

			const canvas = await html2canvas(previewEl, {
				scale: 2,
				useCORS: true,
				backgroundColor: '#1e1e1e'
			});

			// Restore original styles
			for (const s of savedStyles) {
				s.el.style.overflow = s.overflow;
				s.el.style.height = s.height;
				s.el.style.maxHeight = s.maxHeight;
			}

			const imgWidth = 210; // A4 mm
			const pageHeight = 297;
			const margin = 10;
			const contentWidth = imgWidth - margin * 2;
			const imgHeight = (canvas.height * contentWidth) / canvas.width;

			const pdf = new jsPDF('p', 'mm', 'a4');
			let y = margin;
			let remaining = imgHeight;
			const pageContentHeight = pageHeight - margin * 2;

			while (remaining > 0) {
				if (y !== margin) pdf.addPage();
				pdf.addImage(
					canvas.toDataURL('image/png'),
					'PNG',
					margin,
					y === margin ? margin : margin - (imgHeight - remaining),
					contentWidth,
					imgHeight
				);
				remaining -= pageContentHeight;
				y = margin;
			}

			const name = selectedSpec.name.replace(/\.md$/, '');
			pdf.save(`${name}.pdf`);
		} catch (e) {
			console.error('PDF export failed:', e);
		} finally {
			pdfExporting = false;
		}
	}

	onMount(() => {
		loadSpecs();
	});
</script>

{#if selectedSpec}
	<!-- Detail View -->
	<div class="spec-detail">
		<div class="spec-detail-header">
			<button class="btn-back" onclick={goBack}>Back</button>
			<span class="spec-detail-title">{selectedSpec.name}</span>
			{#if detectType(selectedSpec.name)}
				<span class="spec-type-badge" style="background:{typeColors[detectType(selectedSpec.name)]}20;color:{typeColors[detectType(selectedSpec.name)]}">{detectType(selectedSpec.name)}</span>
			{/if}
			<div class="wf-controls">
				<select class="wf-preset-select" bind:value={currentPreset} onchange={onPresetChange}>
					{#each presetEntries as [key, p]}
						<option value={key}>{p.label}</option>
					{/each}
				</select>
				<label class="wf-toggle-label">
					<input type="checkbox" bind:checked={compactMode} onchange={onToggleChange} />
					Compact
				</label>
				<label class="wf-toggle-label">
					<input type="checkbox" bind:checked={showGrid} onchange={onToggleChange} />
					Grid
				</label>
			</div>
			<button class="btn-download" onclick={downloadPDF} disabled={pdfExporting}>
				{pdfExporting ? 'Exporting...' : 'PDF'}
			</button>
			<button class="btn-download" onclick={downloadSpec}>Download</button>
		</div>
		<div class="spec-preview markdown-body" bind:this={previewEl}>
			{#if renderedHTML}
				{@html renderedHTML}
			{:else}
				<p class="text-muted">Loading...</p>
			{/if}
		</div>
	</div>
{:else}
	<!-- List View -->
	<div class="spec-list-container">
		{#if loading}
			<p class="text-muted">Loading specs...</p>
		{:else if specs.length === 0}
			<div class="spec-empty">
				<p class="text-muted">No spec files found.</p>
				<p class="text-muted" style="font-size:12px;margin-top:4px">
					Create spec files in the <code>spec/</code> directory or use <code>shepherd spec generate</code>
				</p>
			</div>
		{:else}
			<div class="spec-list">
				{#each specs as s (s.path)}
					<button class="card spec-card" onclick={() => openSpec(s)}>
						<div class="sc-row">
							<span class="sc-name">{s.name}</span>
							{#if detectType(s.name)}
								<span class="spec-type-badge" style="background:{typeColors[detectType(s.name)]}20;color:{typeColors[detectType(s.name)]}">{detectType(s.name)}</span>
							{/if}
						</div>
						<div class="sc-meta">
							<span>{formatSize(s.size)}</span>
							<span>{s.modified_at}</span>
						</div>
					</button>
				{/each}
			</div>
		{/if}
	</div>
{/if}

<style>
	.spec-list-container {
		padding: 8px 0;
		overflow-y: auto;
		flex: 1;
	}

	.spec-empty {
		text-align: center;
		padding: 32px 16px;
	}

	.spec-empty code {
		background: var(--bg-tertiary, #2a2a2a);
		padding: 2px 6px;
		border-radius: 3px;
		font-size: 12px;
	}

	.spec-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.spec-card {
		display: block;
		width: 100%;
		text-align: left;
		cursor: pointer;
		font: inherit;
		color: inherit;
		transition: border-color 0.15s;
		padding: 10px 14px;
	}

	.spec-card:hover {
		border-color: var(--accent);
	}

	.sc-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 4px;
	}

	.sc-name {
		font-weight: 600;
		font-size: 13px;
	}

	.sc-meta {
		display: flex;
		gap: 10px;
		font-size: 11px;
		color: var(--text-secondary, #888);
	}

	.spec-type-badge {
		display: inline-block;
		padding: 2px 8px;
		border-radius: 10px;
		font-size: 11px;
		font-weight: 600;
		white-space: nowrap;
		flex-shrink: 0;
	}

	/* Detail View */
	.spec-detail {
		display: flex;
		flex-direction: column;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}

	.spec-detail-header {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 8px 0;
		flex-shrink: 0;
	}

	.spec-detail-title {
		font-size: 14px;
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.btn-back, .btn-download {
		padding: 4px 12px;
		border: 1px solid var(--border-color, #333);
		border-radius: 4px;
		background: transparent;
		color: inherit;
		font-size: 12px;
		cursor: pointer;
		flex-shrink: 0;
	}

	.btn-back:hover, .btn-download:hover {
		background: var(--bg-tertiary, #2a2a2a);
	}

	.btn-download:first-of-type {
		margin-left: 0;
	}

	.wf-controls {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-left: auto;
	}

	.wf-preset-select {
		padding: 3px 8px;
		border: 1px solid var(--border-color, #333);
		border-radius: 4px;
		background: var(--bg-tertiary, #2a2a2a);
		color: inherit;
		font-size: 12px;
		cursor: pointer;
	}

	.wf-toggle-label {
		display: flex;
		align-items: center;
		gap: 4px;
		font-size: 11px;
		color: var(--text-secondary, #888);
		cursor: pointer;
		white-space: nowrap;
	}

	.wf-toggle-label input[type="checkbox"] {
		width: 13px;
		height: 13px;
		cursor: pointer;
	}

	.spec-preview {
		flex: 1;
		overflow-y: auto;
		padding: 16px;
		background: var(--bg-secondary, #1e1e1e);
		border-radius: 8px;
		font-size: 14px;
		line-height: 1.6;
	}

	/* Markdown styles for dark theme */
	.spec-preview :global(h1) { font-size: 1.6em; margin: 0.8em 0 0.4em; border-bottom: 1px solid var(--border-color, #333); padding-bottom: 0.3em; }
	.spec-preview :global(h2) { font-size: 1.3em; margin: 0.8em 0 0.3em; border-bottom: 1px solid var(--border-color, #333); padding-bottom: 0.2em; }
	.spec-preview :global(h3) { font-size: 1.1em; margin: 0.6em 0 0.3em; }
	.spec-preview :global(table) { border-collapse: collapse; width: 100%; margin: 8px 0; font-size: 13px; }
	.spec-preview :global(th), .spec-preview :global(td) { border: 1px solid var(--border-color, #444); padding: 6px 10px; text-align: left; }
	.spec-preview :global(th) { background: var(--bg-tertiary, #2a2a2a); font-weight: 600; }
	.spec-preview :global(pre) { background: var(--bg-tertiary, #1a1a1a); padding: 12px; border-radius: 6px; overflow-x: auto; font-size: 13px; }
	.spec-preview :global(code) { font-family: 'Fira Code', 'Cascadia Code', monospace; font-size: 0.9em; }
	.spec-preview :global(p code) { background: var(--bg-tertiary, #2a2a2a); padding: 2px 5px; border-radius: 3px; }
	.spec-preview :global(ul), .spec-preview :global(ol) { padding-left: 24px; }
	.spec-preview :global(li) { margin: 2px 0; }
	.spec-preview :global(blockquote) { border-left: 3px solid var(--border-color, #444); padding-left: 12px; color: var(--text-muted, #888); margin: 8px 0; }
	.spec-preview :global(a) { color: #3b82f6; }
	.spec-preview :global(hr) { border: none; border-top: 1px solid var(--border-color, #333); margin: 16px 0; }
	.spec-preview :global(img) { max-width: 100%; }

	/* Wireframe container: use CSS variables instead of dark theme overrides */
	.spec-preview :global(.wf-container th) { background: var(--wf-surface-alt); color: var(--wf-text); border-color: var(--wf-border); }
	.spec-preview :global(.wf-container td) { background: var(--wf-surface); color: var(--wf-text); border-color: var(--wf-border); }
	.spec-preview :global(.wf-container table) { color: var(--wf-text); }
	.spec-preview :global(.wf-container pre) { background: var(--wf-surface-alt); color: var(--wf-text); }
	.spec-preview :global(.wf-container code) { color: var(--wf-text); }

	/* Mermaid overrides for dark theme */
	.spec-preview :global(.mermaid) {
		margin: 16px 0;
		text-align: center;
	}
	.spec-preview :global(.mermaid svg) {
		max-width: 100%;
	}

	.text-muted {
		color: var(--text-muted, #888);
		font-size: 13px;
	}
</style>
