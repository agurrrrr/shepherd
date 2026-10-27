// Pixel sprite maps shared by Icon, BrandMark, FlockField and EmptyState.
// A map is an array of equal-length strings: '.' is empty, 'x' paints
// currentColor, any other letter paints var(--px-<letter>) — the sprite
// colour tokens defined in app.css (see docs mockup shared/pixel.js).

export const SHEEP = [
	'...oooooooooo...',
	'..owwwwwwwwwwo..',
	'.owwwwwwwwwwwwo.',
	'.owwoooooooowwo.',
	'ooooffffffffoooo',
	'okkoffffffffokko',
	'.ooofeffffefooo.',
	'.owofpffffpfowo.',
	'.owofffkkfffowo.',
	'.owwoooooooowwo.',
	'.owwwwwwwwwwsso.',
	'..oswwwwwwwsso..',
	'...oooooooooo...',
	'....ll....ll....'
];

// Resting sheep: eyes closed (e-cells where the working face has f-eyes).
export const SHEEP_REST = SHEEP.map((r, i) => (i === 6 ? '.oooeeffffeeooo.' : r));

// The shepherd's crook with a yellow lantern hook (the brand mark).
export const CROOK = [
	'..xxxx...',
	'.x....x..',
	'.x....x..',
	'.xx...x..',
	'......x..',
	'......x..',
	'......x..',
	'......x..',
	'.....xxx.'
];

// Field decorations (mockup 06: stars, moon, fence, grass tufts).
export const DECO = {
	star: ['.y.', 'yyy', '.y.'],
	starBig: ['..y..', '..y..', 'yyyyy', '..y..', '..y..'],
	moon: ['...mmm..', '.mmmm...', 'mmmm....', 'mmm.....', 'mmm.....', 'mmmm....', '.mmmm...', '...mmm..'],
	fence: [
		'..BBB.....',
		'..BbB.....',
		'BBBbBBBBBB',
		'bbbbbbbbbb',
		'BBBbBBBBBB',
		'..BbB.....',
		'..BbB.....',
		'BBBbBBBBBB',
		'bbbbbbbbbb',
		'BBBbBBBBBB',
		'..BbB.....',
		'..BbB.....'
	],
	tuft: ['g...g', 'g.g.g', '.ggg.']
};
