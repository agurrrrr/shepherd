<script>
	let { skill = null, projects = [], fixedProject = '', fixedScope = '', onSave, onCancel } = $props();

	let name = $state(skill?.name ?? '');
	let description = $state(skill?.description ?? '');
	let content = $state(skill?.content ?? '');
	let scope = $state(fixedScope || (skill?.scope ?? 'project'));
	let selectedProject = $state(fixedProject || skill?.project || '');
	let enabled = $state(skill?.enabled ?? true);
	let tagsInput = $state(skill?.tags?.join(', ') ?? '');
	let saving = $state(false);
	let error = $state('');

	async function handleSave() {
		error = '';
		if (!name.trim()) { error = 'Name is required'; return; }
		if (!content.trim()) { error = 'Content is required'; return; }
		if (scope === 'project' && !selectedProject && !fixedProject) {
			error = 'Project is required for project-scoped skills';
			return;
		}

		saving = true;
		try {
			const tags = tagsInput.split(',').map(t => t.trim()).filter(Boolean);
			await onSave({
				name: name.trim(),
				description: description.trim(),
				content: content.trim(),
				scope,
				enabled,
				tags,
				project: fixedProject || selectedProject,
			});
		} catch (e) {
			error = e.message || 'Failed to save';
		}
		saving = false;
	}
</script>

<div class="skill-form">
	{#if error}
		<div class="form-error">{error}</div>
	{/if}

	{#if !fixedScope}
		<div class="form-group">
			<label class="form-label">Scope</label>
			<div class="type-toggle">
				<button class="type-btn" class:active={scope === 'global'} onclick={() => scope = 'global'}>Global</button>
				<button class="type-btn" class:active={scope === 'project'} onclick={() => scope = 'project'}>Project</button>
			</div>
		</div>
	{/if}

	{#if scope === 'project' && !fixedProject}
		<div class="form-group">
			<label class="form-label">Project</label>
			<select class="input" bind:value={selectedProject}>
				<option value="">Select project...</option>
				{#each projects as p}
					<option value={p.name}>{p.name}</option>
				{/each}
			</select>
		</div>
	{/if}

	<div class="form-group">
		<label class="form-label">Name</label>
		<input class="input" type="text" bind:value={name} placeholder="e.g. code-review" />
	</div>

	<div class="form-group">
		<label class="form-label">Description <span class="optional">(optional)</span></label>
		<input class="input" type="text" bind:value={description} placeholder="e.g. Code review checklist" />
	</div>

	<div class="form-group">
		<label class="form-label">Content <span class="content-hint">(Markdown)</span></label>
		<textarea class="input content-editor" bind:value={content} rows="12" placeholder="# Skill Instructions&#10;&#10;Write the skill content in Markdown..."></textarea>
	</div>

	<div class="form-group">
		<label class="form-label">Tags <span class="optional">(comma separated)</span></label>
		<input class="input" type="text" bind:value={tagsInput} placeholder="e.g. review, quality, testing" />
	</div>

	<div class="form-group">
		<label class="form-label checkbox-label">
			<input type="checkbox" bind:checked={enabled} />
			Enabled
		</label>
	</div>

	<div class="form-actions">
		{#if onCancel}
			<button class="btn" onclick={onCancel}>Cancel</button>
		{/if}
		<button class="btn btn-primary" onclick={handleSave} disabled={saving}>
			{saving ? 'Saving...' : (skill ? 'Update' : 'Create')}
		</button>
	</div>
</div>

<style>
	.skill-form {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.form-error {
		color: var(--danger);
		background: rgba(248, 81, 73, 0.1);
		border: 1px solid var(--danger);
		border-radius: var(--radius);
		padding: 8px 12px;
		font-size: 13px;
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.form-label {
		font-size: 13px;
		font-weight: 500;
		color: var(--text-secondary);
	}

	.optional, .content-hint {
		font-weight: 400;
		opacity: 0.6;
	}

	.checkbox-label {
		display: flex;
		align-items: center;
		gap: 6px;
		cursor: pointer;
	}

	.content-editor {
		resize: vertical;
		min-height: 200px;
		font-family: var(--font-mono);
		font-size: 13px;
		line-height: 1.5;
		tab-size: 2;
	}

	.type-toggle {
		display: flex;
		gap: 4px;
	}

	.type-btn {
		padding: 6px 16px;
		border: 1px solid var(--border);
		background: var(--bg-primary);
		color: var(--text-secondary);
		border-radius: var(--radius);
		cursor: pointer;
		font-size: 13px;
	}

	.type-btn.active {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.form-actions {
		display: flex;
		gap: 8px;
		justify-content: flex-end;
		padding-top: 8px;
	}

	.btn-primary {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.btn-primary:hover {
		opacity: 0.9;
	}

	.btn-primary:disabled {
		opacity: 0.5;
	}
</style>
