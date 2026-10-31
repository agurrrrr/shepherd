#!/usr/bin/env node
// gen-icons: draws the favicon, PWA icons and notification badge as Pico
// Night Gray pixel art (wiki webui-pico-night-gray-design-system). The sheep
// and the field decorations come from src/lib/pixel/sprites.js, so the icon
// stays in step with the flock the UI draws.
//
//   npm run gen:icons                  rewrite the PNGs in web/static/
//   npm run gen:icons -- --out <dir>   write them somewhere else (preview)
//
// Every image is a cell grid scaled by a whole number, so each cell stays a
// crisp square. No dependencies: the PNG encoder below uses node:zlib.
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { deflateSync } from 'node:zlib';
import { SHEEP, DECO } from '../src/lib/pixel/sprites.js';

const WEB_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// Colours mirror the tokens in src/app.css.
const COLOR = {
	line: '#000000', // --line
	hi: '#46464c', // --hi (bevel, top-left)
	lo: '#1b1b1e', // --lo (bevel, bottom-right)
	sky: '#000000', // FlockField sky, top band
	dusk: '#161618', // FlockField sky, middle band
	ground: '#2a2a2f' // --bg-2, FlockField bottom band
};

// Sprite letters → --px-* tokens. 'x' is the crook (--text-primary).
const SPRITE = {
	x: '#fff1e8',
	o: '#000000',
	w: '#fff1e8',
	s: '#c2c3c7',
	f: '#ffccaa',
	e: '#000000',
	p: '#ff77a8',
	k: '#ff77a8',
	l: '#000000',
	y: '#ffec27',
	m: '#fff1e8',
	g: '#00e436',
	a: '#ffa300' // --p-orange, the lantern flame
};

// The brand crook, drawn two cells thick so it holds up at launcher size,
// with the yellow lantern of BrandMark hanging from the tip of the hook.
// 'y' is the lantern, 'a' its flame, 's' the metal cap and ring.
const CROOK = [
	'...xxxx..',
	'..xxxxxx.',
	'.xx....xx',
	'.xx....xx',
	'.xx....xx',
	'.ss....xx',
	'ssss...xx',
	'yyyy...xx',
	'yaay...xx',
	'yaay...xx',
	'yyyy...xx',
	'.ss....xx'
];

// A crescent moon small enough to leave the sky to the stars.
const MOON = ['..mmm.', '.mmm..', 'mmm...', 'mmm...', '.mmm..', '..mmm.'];

function shaft(map, height) {
	const last = map[map.length - 1].replace(/[^x]/g, '.');
	const out = [...map];
	while (out.length < height) out.push(last);
	return out;
}

// --- cell grid -------------------------------------------------------------

function grid(w, h = w) {
	return { w, h, cells: Array.from({ length: h }, () => Array(w).fill(null)) };
}

function fill(g, x, y, w, h, color) {
	for (let j = y; j < y + h; j++) {
		for (let i = x; i < x + w; i++) {
			if (i >= 0 && j >= 0 && i < g.w && j < g.h) g.cells[j][i] = color;
		}
	}
}

function stamp(g, map, ox, oy, palette = SPRITE) {
	map.forEach((row, y) => {
		[...row].forEach((c, x) => {
			if (c !== '.') fill(g, ox + x, oy + y, 1, 1, palette[c]);
		});
	});
}

// --- scenes ----------------------------------------------------------------

// The night pasture: sky bands as in FlockField, a moon and stars, the crook
// with its lantern and one sheep. It is 28×28 cells; (ox, oy) is its top-left.
// The bands run the full grid width, and the top and bottom bands run to the
// grid edges, so a full-bleed (maskable) grid has no seams.
function pasture(g, ox, oy) {
	const duskTop = oy + 10;
	const groundTop = oy + 20;
	fill(g, 0, 0, g.w, duskTop, COLOR.sky);
	fill(g, 0, duskTop, g.w, groundTop - duskTop, COLOR.dusk);
	fill(g, 0, groundTop, g.w, g.h - groundTop, COLOR.ground);

	stamp(g, MOON, ox + 21, oy + 2);
	stamp(g, DECO.star, ox + 14, oy + 3);
	stamp(g, ['y'], ox + 18, oy + 8);
	stamp(g, ['y'], ox + 26, oy + 10);

	stamp(g, shaft(CROOK, 26), ox + 1, oy + 1);
	stamp(g, SHEEP, ox + 11, oy + 12);
}

// The UI card edge on the outer cells of `g`: --px-ring (corners left empty)
// and, one cell in, --px-bevel.
function frame(g) {
	const n = g.w;
	for (const [x, y] of [[0, 0], [n - 1, 0], [0, n - 1], [n - 1, n - 1]]) g.cells[y][x] = null;
	fill(g, 1, 0, n - 2, 1, COLOR.line);
	fill(g, 1, n - 1, n - 2, 1, COLOR.line);
	fill(g, 0, 1, 1, n - 2, COLOR.line);
	fill(g, n - 1, 1, 1, n - 2, COLOR.line);
	// --px-bevel lists the light edge first, so it wins the shared corners.
	fill(g, 1, n - 2, n - 2, 1, COLOR.lo);
	fill(g, n - 2, 1, 1, n - 2, COLOR.lo);
	fill(g, 1, 1, n - 2, 1, COLOR.hi);
	fill(g, 1, 1, 1, n - 2, COLOR.hi);
}

// "any" icon: the pasture on a UI card. 32×32 cells.
function tile() {
	const g = grid(32);
	pasture(g, 2, 2);
	frame(g);
	return g;
}

// Full-bleed pasture for launchers that apply their own mask (maskable,
// iOS home screen). `size` cells, the 28-cell scene centred.
function bleed(size) {
	const g = grid(size);
	const o = Math.floor((size - 28) / 2);
	pasture(g, o, o);
	return g;
}

// Favicon: the crook and lantern on a small card. 16×16 cells, so a 32px
// image drops to exact cells at 16 CSS px.
function favicon() {
	const g = grid(16);
	fill(g, 0, 0, 16, 16, COLOR.sky);
	frame(g);
	stamp(g, shaft(CROOK, 12), 3, 2);
	return g;
}

// Notification badge: Android paints only the alpha channel, so this is the
// crook alone, in white on transparent, with the flame cut out so the
// lantern still reads as a lantern. 16×16 cells.
function badge() {
	const g = grid(16);
	const white = Object.fromEntries(Object.keys(SPRITE).map((k) => [k, '#ffffff']));
	white.a = null;
	stamp(g, shaft(CROOK, 12), 3, 2, white);
	return g;
}

// --- raster + PNG ----------------------------------------------------------

// Scale each cell to `px` pixels, then crop `crop` pixels from every edge.
function raster(g, px, crop = 0) {
	const w = g.w * px - crop * 2;
	const h = g.h * px - crop * 2;
	const rgba = Buffer.alloc(w * h * 4);
	for (let y = 0; y < h; y++) {
		for (let x = 0; x < w; x++) {
			const c = g.cells[Math.floor((y + crop) / px)][Math.floor((x + crop) / px)];
			if (!c) continue;
			const i = (y * w + x) * 4;
			rgba[i] = parseInt(c.slice(1, 3), 16);
			rgba[i + 1] = parseInt(c.slice(3, 5), 16);
			rgba[i + 2] = parseInt(c.slice(5, 7), 16);
			rgba[i + 3] = 255;
		}
	}
	return { w, h, rgba };
}

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
	let c = n;
	for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
	return c >>> 0;
});

function crc32(buf) {
	let c = 0xffffffff;
	for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8);
	return (c ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
	const head = Buffer.alloc(8);
	head.writeUInt32BE(data.length, 0);
	head.write(type, 4, 'ascii');
	const crc = Buffer.alloc(4);
	crc.writeUInt32BE(crc32(Buffer.concat([head.subarray(4), data])), 0);
	return Buffer.concat([head, data, crc]);
}

function png({ w, h, rgba }) {
	const ihdr = Buffer.alloc(13);
	ihdr.writeUInt32BE(w, 0);
	ihdr.writeUInt32BE(h, 4);
	ihdr.set([8, 6, 0, 0, 0], 8); // 8-bit RGBA, no interlace
	const raw = Buffer.alloc((w * 4 + 1) * h);
	for (let y = 0; y < h; y++) rgba.copy(raw, y * (w * 4 + 1) + 1, y * w * 4, (y + 1) * w * 4);
	return Buffer.concat([
		Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
		chunk('IHDR', ihdr),
		chunk('IDAT', deflateSync(raw, { level: 9 })),
		chunk('IEND', Buffer.alloc(0))
	]);
}

// --- output ----------------------------------------------------------------

const outArg = process.argv.indexOf('--out');
const OUT = outArg > 0 ? path.resolve(process.argv[outArg + 1]) : path.join(WEB_DIR, 'static');

const FILES = {
	'favicon.png': raster(favicon(), 2), // 32
	'icon-192.png': raster(tile(), 6), // 192
	'icon-512.png': raster(tile(), 16), // 512
	// 44 cells × 12px = 528, cropped to 512. The scene stays inside the 80%
	// safe circle that maskable launchers keep.
	'icon-maskable-512.png': raster(bleed(44), 12, 8),
	// iOS rounds the corners itself and fills transparency with black.
	'apple-touch-icon.png': raster(bleed(36), 5), // 180
	'badge-96.png': raster(badge(), 6) // 96
};

mkdirSync(OUT, { recursive: true });
for (const [name, img] of Object.entries(FILES)) {
	writeFileSync(path.join(OUT, name), png(img));
	console.log(`${name.padEnd(24)} ${img.w}×${img.h}`);
}
