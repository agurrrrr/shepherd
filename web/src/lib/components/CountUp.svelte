<script>
	// Tweened number: counts from the previous value to the new one with an
	// ease-out curve. Reduced-motion users get the final value immediately.
	let { value = 0, duration = 900, format = (n) => Math.round(n).toLocaleString() } = $props();

	let shown = $state(0);
	let from = 0;
	let raf = 0;

	$effect(() => {
		const target = Number(value) || 0;
		const reduce = typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;
		cancelAnimationFrame(raf);
		if (reduce || duration <= 0 || target === from) {
			shown = target;
			from = target;
			return;
		}
		const start = performance.now();
		const begin = from;
		const step = (now) => {
			const t = Math.min(1, (now - start) / duration);
			const eased = 1 - Math.pow(1 - t, 4);
			shown = begin + (target - begin) * eased;
			if (t < 1) raf = requestAnimationFrame(step);
			else from = target;
		};
		raf = requestAnimationFrame(step);
		return () => {
			// Retarget mid-flight from wherever the number currently is.
			cancelAnimationFrame(raf);
			from = shown;
		};
	});
</script>

{format(shown)}
