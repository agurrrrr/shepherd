<script>
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { login } from '$lib/api.js';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import { SHEEP, SHEEP_REST, DECO } from '$lib/pixel/sprites.js';

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
	// Deterministic scatter so the scene is identical on every visit. Pixel
	// sheep sit on a flat green pasture; a few blink awake like the dashboard.
	function rng(seed) {
		return () => (seed = (seed * 16807) % 2147483647) / 2147483647;
	}
	const r = rng(7);
	const stars = Array.from({ length: 26 }, () => ({
		x: r() * 98,
		y: r() * 40,
		big: r() > 0.6,
		d: (r() * 3).toFixed(2)
	}));
	const flock = Array.from({ length: 12 }, (_, i) => {
		const x = 4 + (i / 12) * 92 + r() * 4;
		return { x, y: 74 + r() * 16 };
	});

	// A few sheep wake up and fall asleep again, like the real dashboard.
	let awake = $state([2, 6, 9]);
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

	let clock = $derived(now.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }));
	// Same day-part boundaries as the dashboard greeting.
	let watchName = $derived.by(() => {
		const h = now.getHours();
		if (h >= 5 && h < 12) return 'Morning watch';
		if (h >= 12 && h < 18) return 'Day watch';
		if (h >= 18 && h < 23) return 'Evening watch';
		return 'Night watch';
	});

	function rects(map, scale) {
		let out = '';
		map.forEach((row, y) => {
			let x = 0;
			while (x < row.length) {
				const c = row[x];
				if (c === '.') { x++; continue; }
				let run = 1;
				while (row[x + run] === c) run++;
				const fill = c === 'x' ? 'currentColor' : `var(--px-${c})`;
				out += `<rect x="${x * scale}" y="${y * scale}" width="${run * scale}" height="${scale}" fill="${fill}"/>`;
				x += run;
			}
		});
		return out;
	}
</script>

<svelte:head>
	<title>Sign in · Shepherd</title>
</svelte:head>

<div class="login-page">
	<!-- ============ Scene ============ -->
	<section class="scene" aria-hidden="true">
		<div class="sky"></div>
		<div class="ground"></div>

		{#each stars as st, i (i)}
			<span class="star" class:big={st.big} style="left: {st.x}%; top: {st.y}%; --d: {st.d}s">
				<svg viewBox="0 0 {st.big ? 5 : 3} {st.big ? 5 : 3}" width={(st.big ? 5 : 3) * 2} height={(st.big ? 5 : 3) * 2} shape-rendering="crispEdges">
					{@html rects(st.big ? DECO.starBig : DECO.star, 1)}
				</svg>
			</span>
		{/each}

		<div class="moon">
			<svg viewBox="0 0 {DECO.moon[0].length} {DECO.moon.length}" width={DECO.moon[0].length * 5} height={DECO.moon.length * 5} shape-rendering="crispEdges">
				{@html rects(DECO.moon, 1)}
			</svg>
		</div>

		{#each flock as sh, i (i)}
			<span class="sheep" class:awake={awake.includes(i)} style="left: {sh.x}%; top: {sh.y}%"></span>
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
				Pixel sheep do the chores: issues, tasks, schedules and wikis — while you keep the lantern.
			</p>
		</div>

		<footer class="scene-foot">
			<span>Shepherd</span>
			<span class="sep"></span>
			<span>AI coding orchestration</span>
		</footer>
	</section>

	<!-- ============ Panel ============ -->
	<main class="panel">
		<form class="login-card" onsubmit={handleSubmit}>
			<div class="brand">
				<BrandMark size={34} live={!loading} />
				<span class="sheep-mark">
					<svg viewBox="0 0 {SHEEP[0].length} {SHEEP.length}" width={SHEEP[0].length * 2} height={SHEEP.length * 2} shape-rendering="crispEdges">
						{@html rects(SHEEP, 1)}
					</svg>
				</span>
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
				<input id="username" class="input" type="text" autocomplete="username" bind:value={id} placeholder="admin" disabled={loading} />
			</div>

			<div class="form-group">
				<label for="password">Password</label>
				<input id="password" class="input" type="password" autocomplete="current-password" bind:value={password} placeholder="••••••••" disabled={loading} />
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
		box-shadow: 3px 0 0 0 #000;
	}

	.sky {
		position: absolute;
		inset: 0 0 auto 0;
		height: 66%;
		background: linear-gradient(180deg, #000 0 36%, #161618 36% 70%, var(--bg-2) 70%);
	}

	.ground {
		position: absolute;
		inset: auto 0 0 0;
		height: 34%;
		background:
			linear-gradient(180deg, var(--p-green) 0 3px, #00a85a 3px 6px, transparent 6px),
			radial-gradient(circle at 2px 2px, #006e43 1.5px, transparent 2px) 0 0 / 16px 12px,
			var(--p-dgreen);
	}

	.star {
		position: absolute;
		color: var(--p-yellow);
		animation: px-blink calc(1.6s + var(--d)) steps(1) infinite;
	}

	.star svg { display: block; }

	.moon {
		position: absolute;
		right: 9%;
		top: 8%;
		color: var(--p-white);
	}

	.moon svg { display: block; image-rendering: pixelated; }

	/* Small pixel sheep drawn with a repeating square — the flock at rest. */
	.sheep {
		position: absolute;
		z-index: 1;
		width: 16px;
		height: 14px;
		margin: -7px 0 0 -8px;
		background: var(--p-white);
		box-shadow: inset -5px -4px 0 0 var(--p-lgray), inset 3px 2px 0 0 #000, 0 2px 0 0 #000;
		opacity: 0.55;
		animation: px-blink calc(3.2s + var(--d, 0s)) steps(1) infinite;
	}

	.sheep.awake {
		background: var(--p-blue);
		opacity: 1;
		animation: none;
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

	/* 48px keeps both Press Start 2P and its Hangul fallback on the pixel grid. */
	.scene-title {
		font-family: var(--font-display);
		font-size: var(--fs-3xl);
		font-weight: 400;
		line-height: 1.5;
		letter-spacing: 0;
		color: var(--text-primary);
		text-shadow: 4px 4px 0 #000;
		animation: rise-in 900ms var(--ease-out) 80ms backwards;
	}

	.scene-title em {
		font-style: normal;
		color: var(--highlight);
		text-shadow: 4px 4px 0 var(--p-plum);
	}

	.scene-sub {
		max-width: 44ch;
		font-size: var(--fs-md);
		line-height: var(--lh-base);
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
		height: 3px;
		background: var(--hi);
	}

	/* ================= Panel ================= */
	.panel {
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 48px clamp(24px, 5vw, 72px);
		background: var(--bg-1);
	}

	.login-card {
		width: 100%;
		max-width: 360px;
		display: flex;
		flex-direction: column;
		gap: 18px;
		padding: 26px;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel), var(--shadow-2);
		animation: rise-in 800ms var(--ease-out) 120ms backwards;
	}

	.brand {
		display: flex;
		align-items: center;
		gap: 10px;
		margin-bottom: 18px;
	}

	.sheep-mark svg { display: block; image-rendering: pixelated; }

	.brand-word {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		line-height: 1.5;
		color: var(--highlight);
		text-shadow: 2px 2px 0 var(--p-plum);
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
		letter-spacing: 0;
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
		background: var(--danger);
		color: #000;
		box-shadow: var(--px-ring-2);
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
	}

	.login-btn {
		width: 100%;
		height: 46px;
		margin-top: 6px;
		font-size: var(--fs-base);
	}

	.spinner {
		width: 14px;
		height: 14px;
		border: 3px solid transparent;
		background: currentColor;
		box-shadow: inset 0 0 0 3px var(--btn-bg);
		animation: blink-half 700ms steps(2) infinite;
	}

	@keyframes blink-half {
		0%, 49% { opacity: 1; }
		50%, 100% { opacity: 0.35; }
	}

	.hint {
		margin-top: 8px;
		font-size: var(--fs-2xs);
		line-height: 1.6;
		color: var(--text-tertiary);
	}

	.hint code {
		font-size: var(--fs-2xs);
		color: var(--text-secondary);
	}

	/* ================= Responsive ================= */
	@media (max-width: 900px) {
		.login-page {
			grid-template-columns: 1fr;
			grid-template-rows: minmax(320px, 46vh) auto;
		}

		.scene {
			box-shadow: 0 3px 0 0 #000;
		}

		.scene-copy {
			top: 36px;
			gap: 14px;
		}

		.scene-title {
			font-size: var(--fs-2xl);
		}

		.scene-sub,
		.scene-foot {
			display: none;
		}

		.panel {
			align-items: flex-start;
			padding: 32px 24px 48px;
		}

		.brand .sheep-mark {
			display: none;
		}
	}
</style>
