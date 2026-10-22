<script>
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { login } from '$lib/api.js';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import Icon from '$lib/components/Icon.svelte';

	let id = $state('');
	let password = $state('');
	let error = $state('');
	let loading = $state(false);

	async function handleSubmit(e) {
		e?.preventDefault();
		if (loading) return;
		error = '';
		loading = true;

		const result = await login(id, password);

		if (result.success) {
			goto('/');
		} else {
			error = result.error;
		}
		loading = false;
	}

	// --- Night pasture -------------------------------------------------
	// Deterministic scatter so the scene is identical on every visit.
	function rng(seed) {
		return () => (seed = (seed * 16807) % 2147483647) / 2147483647;
	}
	const r = rng(7);
	const stars = Array.from({ length: 64 }, () => ({
		x: r() * 100,
		y: r() * 46,
		s: 1 + r() * 1.6,
		d: (r() * 6).toFixed(2),
		t: (3 + r() * 5).toFixed(2)
	}));
	const flock = Array.from({ length: 44 }, () => {
		const x = 3 + r() * 94;
		const crest = 70 + Math.sin((x / 100) * Math.PI * 1.3 + 0.5) * 4;
		return { x, y: crest + 4 + r() * 18, d: (r() * 6).toFixed(2), t: (4 + r() * 3).toFixed(2) };
	});

	// A few sheep wake up and fall asleep again, like the real dashboard.
	let awake = $state([5, 19, 33]);
	let lamp = $state({ x: 38, y: 62 });
	let now = $state(new Date());

	onMount(() => {
		const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
		const tick = setInterval(() => (now = new Date()), 30_000);
		if (reduce) return () => clearInterval(tick);
		const iv = setInterval(() => {
			const next = Math.floor(Math.random() * flock.length);
			awake = [...awake.slice(1), next];
		}, 2600);
		return () => {
			clearInterval(iv);
			clearInterval(tick);
		};
	});

	function onMove(e) {
		const rect = e.currentTarget.getBoundingClientRect();
		lamp = {
			x: ((e.clientX - rect.left) / rect.width) * 100,
			y: ((e.clientY - rect.top) / rect.height) * 100
		};
	}

	let clock = $derived(now.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }));
	// Same day-part boundaries as the dashboard greeting.
	let watchName = $derived.by(() => {
		const h = now.getHours();
		if (h >= 5 && h < 12) return 'Morning watch';
		if (h >= 12 && h < 18) return 'Day watch';
		if (h >= 18 && h < 23) return 'Evening watch';
		return 'Night watch';
	});
</script>

<svelte:head>
	<title>Sign in · Shepherd</title>
</svelte:head>

<div class="login-page">
	<!-- ============ Scene ============ -->
	<section class="scene" onpointermove={onMove} style="--lx: {lamp.x}%; --ly: {lamp.y}%" aria-hidden="true">
		<div class="sky"></div>
		<div class="lamp"></div>

		{#each stars as st}
			<span class="star" style="left: {st.x}%; top: {st.y}%; --s: {st.s}px; --d: {st.d}s; --t: {st.t}s"></span>
		{/each}

		<svg class="hills" viewBox="0 0 100 40" preserveAspectRatio="none">
			<defs>
				<linearGradient id="hill-back" x1="0" y1="0" x2="0" y2="1">
					<stop offset="0" stop-color="#17171a" />
					<stop offset="1" stop-color="#0d0d0f" />
				</linearGradient>
				<linearGradient id="hill-front" x1="0" y1="0" x2="0" y2="1">
					<stop offset="0" stop-color="#1b1a19" />
					<stop offset="1" stop-color="#0b0b0c" />
				</linearGradient>
			</defs>
			<path d="M0 12 C 18 2, 34 4, 52 10 S 84 18, 100 6 L100 40 L0 40 Z" fill="url(#hill-back)" />
			<path d="M0 22 C 14 14, 30 12, 46 17 S 78 28, 100 18 L100 40 L0 40 Z" fill="url(#hill-front)" />
			<path d="M0 22 C 14 14, 30 12, 46 17 S 78 28, 100 18" fill="none" stroke="rgba(244,184,96,0.12)" stroke-width="0.15" vector-effect="non-scaling-stroke" />
		</svg>

		{#each flock as sh, i}
			<span
				class="sheep"
				class:awake={awake.includes(i)}
				style="left: {sh.x}%; top: {sh.y}%; --d: {sh.d}s; --t: {sh.t}s"
			></span>
		{/each}

		<div class="scene-copy">
			<span class="eyebrow scene-eyebrow">
				<span class="live-dot"></span>
				{watchName} · {clock}
			</span>
			<h1 class="scene-title">
				Keep watch<br />over <em>your flock.</em>
			</h1>
			<p class="scene-sub">
				Shepherd runs a flock of AI coding agents across your projects.
				Hand out the work, watch it happen live, and step in whenever you need to.
			</p>
		</div>

		<footer class="scene-foot">
			<span>Shepherd</span>
			<span class="sep"></span>
			<span>AI coding orchestration</span>
		</footer>
	</section>

	<!-- ============ Form ============ -->
	<main class="panel">
		<form class="login-card" onsubmit={handleSubmit}>
			<div class="brand">
				<BrandMark size={36} live />
				<span class="brand-word">Shepherd</span>
			</div>

			<div class="login-header">
				<h2>Welcome back</h2>
				<p class="login-subtitle">Sign in to tend the flock.</p>
			</div>

			{#if error}
				<div class="login-error" role="alert">
					<Icon name="alert-triangle" size={14} />
					<span>{error}</span>
				</div>
			{/if}

			<div class="form-group">
				<label for="username">Username</label>
				<input
					id="username"
					class="input"
					type="text"
					autocomplete="username"
					bind:value={id}
					placeholder="admin"
					disabled={loading}
				/>
			</div>

			<div class="form-group">
				<label for="password">Password</label>
				<input
					id="password"
					class="input"
					type="password"
					autocomplete="current-password"
					bind:value={password}
					placeholder="••••••••"
					disabled={loading}
				/>
			</div>

			<button type="submit" class="btn btn-primary btn-lg login-btn" disabled={loading}>
				{#if loading}
					<span class="spinner" aria-hidden="true"></span>
					Opening the gate…
				{:else}
					Enter the fold
					<Icon name="arrow-right" size={16} stroke={2.2} />
				{/if}
			</button>

			<p class="hint">Forgot the password? Run <code>shepherd auth change-password</code> on the host.</p>
		</form>
	</main>
</div>

<style>
	.login-page {
		display: grid;
		grid-template-columns: minmax(0, 1.35fr) minmax(400px, 1fr);
		min-height: 100vh;
		min-height: 100dvh;
		background: var(--bg-base);
	}

	/* ================= Scene ================= */
	.scene {
		position: relative;
		overflow: hidden;
		isolation: isolate;
		border-right: 1px solid var(--border-subtle);
	}

	.sky {
		position: absolute;
		inset: 0;
		z-index: -2;
		background:
			radial-gradient(120% 70% at 75% 0%, rgba(143, 193, 255, 0.07), transparent 60%),
			radial-gradient(90% 60% at 10% 10%, rgba(244, 184, 96, 0.06), transparent 60%),
			linear-gradient(180deg, #0b0c10 0%, #0d0d10 55%, #0c0b0b 100%);
	}

	.lamp {
		position: absolute;
		inset: 0;
		z-index: -1;
		background: radial-gradient(420px 320px at var(--lx) var(--ly), rgba(244, 184, 96, 0.14), transparent 70%);
		transition: background 80ms linear;
		pointer-events: none;
	}

	.star {
		position: absolute;
		width: var(--s);
		height: var(--s);
		border-radius: 50%;
		background: #f5efe4;
		opacity: 0.25;
		animation: twinkle var(--t) ease-in-out var(--d) infinite;
	}

	@keyframes twinkle {
		0%, 100% { opacity: 0.12; }
		50% { opacity: 0.7; }
	}

	.hills {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
		width: 100%;
		height: 46%;
		z-index: 0;
	}

	.sheep {
		position: absolute;
		z-index: 1;
		width: 7px;
		height: 7px;
		margin: -3.5px 0 0 -3.5px;
		border-radius: 50%;
		background: var(--wool);
		opacity: 0.35;
		animation: breathe var(--t) ease-in-out var(--d) infinite;
		transition: background 900ms ease, box-shadow 900ms ease, opacity 900ms ease, transform 900ms var(--ease-spring);
	}

	.sheep.awake {
		background: var(--live);
		opacity: 1;
		transform: scale(1.35);
		box-shadow: 0 0 0 4px rgba(95, 224, 194, 0.12), 0 0 18px 2px rgba(95, 224, 194, 0.55);
		animation: none;
	}

	@keyframes breathe {
		0%, 100% { opacity: 0.22; }
		50% { opacity: 0.5; }
	}

	.scene-copy {
		position: absolute;
		left: clamp(32px, 6vw, 88px);
		right: clamp(32px, 6vw, 88px);
		top: clamp(48px, 12vh, 140px);
		display: flex;
		flex-direction: column;
		gap: 22px;
		z-index: 2;
	}

	.scene-eyebrow {
		display: inline-flex;
		align-items: center;
		gap: 10px;
		animation: rise-in 700ms var(--ease-out) backwards;
	}

	.scene-title {
		font-family: var(--font-display);
		font-size: clamp(56px, 7vw, 108px);
		font-weight: 400;
		line-height: 0.9;
		letter-spacing: -0.03em;
		color: var(--text-primary);
		animation: rise-in 900ms var(--ease-out) 80ms backwards;
	}

	.scene-title em {
		font-style: italic;
		color: transparent;
		background: linear-gradient(100deg, #ffe0ad 0%, var(--accent) 45%, #e9925a 100%);
		-webkit-background-clip: text;
		background-clip: text;
		padding-right: 0.05em;
	}

	.scene-sub {
		max-width: 44ch;
		font-size: var(--fs-md);
		line-height: 1.55;
		color: var(--text-secondary);
		animation: rise-in 900ms var(--ease-out) 180ms backwards;
	}

	.scene-foot {
		position: absolute;
		left: clamp(32px, 6vw, 88px);
		bottom: 28px;
		z-index: 2;
		display: flex;
		align-items: center;
		gap: 12px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.sep {
		width: 24px;
		height: 1px;
		background: var(--border-strong);
	}

	/* ================= Panel ================= */
	.panel {
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 48px clamp(24px, 5vw, 72px);
		background:
			radial-gradient(80% 50% at 50% 0%, rgba(244, 184, 96, 0.05), transparent 70%),
			var(--bg-1);
	}

	.login-card {
		width: 100%;
		max-width: 360px;
		display: flex;
		flex-direction: column;
		gap: 18px;
		animation: rise-in 800ms var(--ease-out) 120ms backwards;
	}

	.brand {
		display: flex;
		align-items: center;
		gap: 10px;
		margin-bottom: 18px;
	}

	.brand-word {
		font-family: var(--font-display);
		font-size: 30px;
		line-height: 1;
		color: var(--text-primary);
	}

	.login-header {
		display: flex;
		flex-direction: column;
		gap: 6px;
		margin-bottom: 4px;
	}

	.login-header h2 {
		font-size: var(--fs-xl);
		font-weight: var(--fw-semibold);
		letter-spacing: -0.02em;
	}

	.login-subtitle {
		color: var(--text-secondary);
		font-size: var(--fs-sm);
	}

	.login-error {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 10px 12px;
		border-radius: var(--radius);
		background: var(--danger-soft);
		box-shadow: inset 0 0 0 1px var(--danger-line);
		color: var(--danger);
		font-size: var(--fs-sm);
		animation: shake 420ms var(--ease-out);
	}

	@keyframes shake {
		20% { transform: translateX(-6px); }
		40% { transform: translateX(5px); }
		60% { transform: translateX(-3px); }
		80% { transform: translateX(2px); }
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 7px;
	}

	.form-group label {
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		color: var(--text-secondary);
	}

	.form-group .input {
		width: 100%;
		height: 44px;
		padding: 0 14px;
		border-radius: var(--radius-md);
	}

	.login-btn {
		width: 100%;
		height: 46px;
		margin-top: 6px;
		font-size: var(--fs-base);
	}

	.login-btn :global(.icon) {
		transition: transform var(--dur-2) var(--ease-out);
	}

	.login-btn:hover :global(.icon) {
		transform: translateX(3px);
	}

	.spinner {
		width: 14px;
		height: 14px;
		border-radius: 50%;
		border: 2px solid rgba(26, 18, 6, 0.25);
		border-top-color: currentColor;
		animation: spin 700ms linear infinite;
	}

	.hint {
		margin-top: 8px;
		font-size: var(--fs-2xs);
		line-height: 1.6;
		color: var(--text-tertiary);
	}

	.hint code {
		font-size: 10.5px;
		color: var(--text-secondary);
	}

	/* ================= Responsive ================= */
	@media (max-width: 900px) {
		.login-page {
			grid-template-columns: 1fr;
			grid-template-rows: minmax(320px, 46vh) auto;
		}

		.scene {
			border-right: none;
			border-bottom: 1px solid var(--border-subtle);
		}

		.scene-copy {
			top: 36px;
			gap: 14px;
		}

		.scene-title {
			font-size: clamp(44px, 12vw, 64px);
		}

		.scene-sub,
		.scene-foot {
			display: none;
		}

		.panel {
			align-items: flex-start;
			padding: 32px 24px 48px;
		}

		.brand {
			display: none;
		}
	}
</style>
