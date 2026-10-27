<script>
	// Page header: 8px pixel eyebrow (ASCII only), 24px pixel title with an
	// optional count, one line of context, and right-aligned actions.
	let { title = '', eyebrow = '', subtitle = '', count = null, children } = $props();
</script>

<header class="page-head">
	<div class="page-head-text">
		{#if eyebrow}
			<span class="eyebrow">{eyebrow}</span>
		{/if}
		<h1 class="page-head-title">
			{title}{#if count != null}<span class="page-head-count num">{count}</span>{/if}
		</h1>
		{#if subtitle}
			<p class="page-head-sub">{subtitle}</p>
		{/if}
	</div>
	{#if children}
		<div class="page-head-actions">
			{@render children()}
		</div>
	{/if}
</header>
