<script>
	let { status = 'idle', taskId = null } = $props();

	const statusConfig = {
		idle: { label: 'Idle', css: 'badge-idle' },
		working: { label: 'Working', css: 'badge-working' },
		error: { label: 'Error', css: 'badge-error' },
		pending: { label: 'Pending', css: 'badge-pending' },
		running: { label: 'Running', css: 'badge-running' },
		completed: { label: 'Completed', css: 'badge-completed' },
		failed: { label: 'Failed', css: 'badge-failed' },
		stopped: { label: 'Stopped', css: 'badge-stopped' }
	};

	let config = $derived(statusConfig[status] || statusConfig.idle);
	let displayLabel = $derived(
		taskId ? `${config.label} #${taskId}` : config.label
	);
</script>

<span class="badge {config.css}">{displayLabel}</span>
