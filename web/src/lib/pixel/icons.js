// Pixel maps for every icon the UI draws (grid <= 9x9, '.' empty, 'x' = currentColor).
// The mockup set (layout-dashboard, folder, clock, book-open, settings, log-out,
// check, x, hourglass, stop, arrow-right, menu) comes from docs/design/retro-pixel/
// shared/pixel.js; the rest are drawn on the same 1px grid.

export const PIXEL_ICONS = {
	'menu': [
		'xxxxxxxxx',
		'.........',
		'.........',
		'xxxxxxxxx',
		'.........',
		'.........',
		'xxxxxxxxx'
	],
	'layout-dashboard': [
		'xxxx.xxxx',
		'x..x.x..x',
		'x..x.x..x',
		'xxxx.xxxx',
		'.........',
		'xxxx.xxxx',
		'x..x.x..x',
		'x..x.x..x',
		'xxxx.xxxx'
	],
	'sheep': [
		'..oooooo.',
		'.owwwwwwo',
		'owwwwwwwk',
		'ooffffwfk',
		'.offfwfk.',
		'.owwwwwk.',
		'..o.o.o..'
	],
	'folder': [
		'xxxx.....',
		'x..xxxxxx',
		'x.......x',
		'xxxxxxxxx',
		'x.......x',
		'x.......x',
		'x.......x',
		'xxxxxxxxx'
	],
	'clock': [
		'..xxxxx..',
		'.x.....x.',
		'x...x...x',
		'x...x...x',
		'x...xxx.x',
		'x.......x',
		'.x.....x.',
		'..xxxxx..'
	],
	'alarm-clock': [
		'..xxxxx..',
		'.x.....x.',
		'x...x...x',
		'x...xxx.x',
		'x.......x',
		'.x.....x.',
		'..xxxxx..',
		'.xx...xx.'
	],
	'list-checks': [
		'xx.xxxxxx',
		'xx.......',
		'.........',
		'xx.xxxxxx',
		'xx.......',
		'.........',
		'xx.xxxx..',
		'xx.......'
	],
	'book-open': [
		'xxxx.xxxx',
		'x..xxx..x',
		'x...x...x',
		'x...x...x',
		'x...x...x',
		'x...x...x',
		'xxxxxxxxx'
	],
	'settings': [
		'....x....',
		'.x.xxx.x.',
		'..xxxxx..',
		'.xxx.xxx.',
		'xxx...xxx',
		'.xxx.xxx.',
		'..xxxxx..',
		'.x.xxx.x.',
		'....x....'
	],
	'database': [
		'.xxxxxxx.',
		'x.......x',
		'xxxxxxxxx',
		'x.......x',
		'xxxxxxxxx',
		'x.......x',
		'xxxxxxxxx',
		'x.......x',
		'.xxxxxxx.'
	],
	'check': [
		'......x..',
		'.....xx..',
		'x...xx...',
		'xx.xx....',
		'.xxx.....',
		'..x......'
	],
	'x': [
		'xx...xx',
		'.xx.xx.',
		'..xxx..',
		'.xx.xx.',
		'xx...xx'
	],
	'hourglass': [
		'xxxxxxx',
		'.x...x.',
		'..xxx..',
		'...x...',
		'..x.x..',
		'.x...x.',
		'xxxxxxx'
	],
	'stop': [
		'xxxxx',
		'xxxxx',
		'xxxxx',
		'xxxxx',
		'xxxxx'
	],
	'play': [
		'x....',
		'xxx..',
		'xxxxx',
		'xxx..',
		'x....'
	],
	'square': [
		'xxxxxxx',
		'x.....x',
		'x.....x',
		'x.....x',
		'x.....x',
		'x.....x',
		'xxxxxxx'
	],
	'check-circle': [
		'..xxxxx..',
		'.xx...xx.',
		'xx.....xx',
		'xx..x..xx',
		'xx.x.x.xx',
		'.xxx.xx..',
		'..xxx....',
		'.xx...xx.',
		'..xxxxx..'
	],
	'x-circle': [
		'..xxxxx..',
		'.xx...xx.',
		'xx.x.x.xx',
		'xx..x..xx',
		'xx..x..xx',
		'xx..x..xx',
		'xx.x.x.xx',
		'.xx...xx.',
		'..xxxxx..'
	],
	'circle': [
		'..xxxxx..',
		'.xx...xx.',
		'xx.....xx',
		'xx.....xx',
		'xx.....xx',
		'xx.....xx',
		'xx.....xx',
		'.xx...xx.',
		'..xxxxx..'
	],
	'pause-circle': [
		'..xxxxx..',
		'.xx...xx.',
		'xx.....xx',
		'xx.x.x.xx',
		'xx.x.x.xx',
		'xx.x.x.xx',
		'xx.....xx',
		'.xx...xx.',
		'..xxxxx..'
	],
	'stop-circle': [
		'..xxxxx..',
		'.xx...xx.',
		'xx.....xx',
		'xx.xxx.xx',
		'xx.xxx.xx',
		'xx.xxx.xx',
		'xx.....xx',
		'.xx...xx.',
		'..xxxxx..'
	],
	'alert-triangle': [
		'....x....',
		'...xxx...',
		'..x...x..',
		'..x...x..',
		'.x.x.x.x.',
		'.x.....x.',
		'xxxxxxxxx',
		'xxxxxxxxx'
	],
	'chevron-right': ['x..', 'xx.', '.xx', 'xx.', 'x..'],
	'chevron-left': ['..x', '.xx', 'xx.', '.xx', '..x'],
	'chevron-down': ['xx..xx', '.xxxx.', '..xx..'],
	'chevron-up': ['..xx..', '.xxxx.', 'xx..xx'],
	'arrow-right': [
		'....x..',
		'....xx.',
		'xxxxxxx',
		'....xx.',
		'....x..'
	],
	'arrow-left': [
		'..x....',
		'.xx....',
		'xxxxxxx',
		'.xx....',
		'..x....'
	],
	'arrow-up-right': [
		'...xxxx',
		'...x..x',
		'..x...x',
		'.x....x',
		'x.....x',
		'x....x.',
		'x...x..'
	],
	'plus': [
		'....x....',
		'....x....',
		'....x....',
		'xxxxxxxxx',
		'....x....',
		'....x....',
		'....x....'
	],
	'edit': [
		'.......xx',
		'.....xxx.',
		'...xxx...',
		'.xxx.....',
		'xxx......',
		'xx.......',
		'x........'
	],
	'trash': [
		'..xxxxx..',
		'....x....',
		'.xxxxxxx.',
		'.x.....x.',
		'.x.....x.',
		'.x.....x.',
		'.x.....x.',
		'.xxxxxxx.'
	],
	'copy': [
		'..xxxx...',
		'..x...x..',
		'..x.xxx.x',
		'....x..x.',
		'....x..x.',
		'....x..x.',
		'....xxx..'
	],
	'download': [
		'....x....',
		'....x....',
		'....x....',
		'.x..x..x.',
		'..x.x.x..',
		'...xxx...',
		'xxxxxxxx.'
	],
	'upload': [
		'...xxx...',
		'..x.x.x..',
		'.x..x..x.',
		'....x....',
		'....x....',
		'....x....',
		'xxxxxxxx.'
	],
	'refresh-cw': [
		'..xxxxxx.',
		'.x.....xx',
		'xx......x',
		'x........',
		'x........',
		'x......xx',
		'xx.....x.',
		'.xxxxxx..',
		'.....xx..'
	],
	'rotate-ccw': [
		'.xxxxxx..',
		'xx.....x.',
		'x......xx',
		'........x',
		'........x',
		'xx......x',
		'.x.....xx',
		'..xxxxxx.',
		'..xx.....'
	],
	'search': [
		'.xxx.....',
		'x...x....',
		'x...x....',
		'.xxx.....',
		'...xx....',
		'....xx...',
		'.....xx..',
		'......xx.'
	],
	'filter': [
		'xxxxxxxxx',
		'.x.....x.',
		'..x...x..',
		'...x.x...',
		'....x....',
		'....x....',
		'....x....'
	],
	'corner-down-left': [
		'.....xxx.',
		'....xx...',
		'.xxx.....',
		'xxx......',
		'.xxx.....',
		'....xx...',
		'.....xxx.'
	],
	'file': [
		'xxxxx....',
		'x...xx...',
		'x....x...',
		'x....x...',
		'x....x...',
		'x....x...',
		'xxxxxxx..'
	],
	'file-text': [
		'xxxxx....',
		'x...xx...',
		'x....x...',
		'x.xxx.x..',
		'x.....x..',
		'x.xxx.x..',
		'xxxxxxx..'
	],
	'code': [
		'...x...x.',
		'..x...x..',
		'.x.....x.',
		'.........',
		'.x.....x.',
		'..x...x..',
		'...x...x.'
	],
	'braces': [
		'..xx..xx.',
		'.x....x..',
		'.x....x..',
		'xxx..xxx.',
		'.x....x..',
		'.x....x..',
		'..xx..xx.'
	],
	'image': [
		'xxxxxxxxx',
		'x.x.....x',
		'xxxx...xx',
		'xxxxx..xx',
		'xxxxxx.xx',
		'xxxxxxx.x',
		'xxxxxxxxx'
	],
	'inbox': [
		'.xxxxxxx.',
		'xx.....xx',
		'xx.....xx',
		'xxx...xxx',
		'.xxxxxxx.',
		'..xx.xx..',
		'x.......x',
		'xxxxxxxxx'
	],
	'message-square': [
		'xxxxxxxxx',
		'x.......x',
		'x.xxxxx.x',
		'x.......x',
		'xxxxxxxxx',
		'.xx......',
		'.x.......'
	],
	'terminal': [
		'.xxx..x..',
		'..x...x..',
		'.x....x..',
		'......x..',
		'xxxxxxx..'
	],
	'zap': [
		'...xxx...',
		'..xxx....',
		'.xxxx....',
		'xxxxxxx..',
		'...xxxx..',
		'..xxx....',
		'.xxx.....',
		'.xx......'
	],
	'command': [
		'..xx...x.',
		'.x..x...x',
		'.x..x...x',
		'...xxx...',
		'...xxx...',
		'.x...x..x',
		'.x...x..x',
		'..xx...xx'
	],
	'layers': [
		'....x....',
		'...xxx...',
		'..xxxxx..',
		'.xxxxxxx.',
		'xxxxxxxxx',
		'.........',
		'.xxxxxxx.',
		'..xxxxx..'
	],
	'sparkles': [
		'....x....',
		'....x....',
		'..x.xx.x.',
		'...xxx...',
		'xxxx.xxx.',
		'...xxx...',
		'..x.xx.x.',
		'....x....',
		'....x....'
	],
	'bug': [
		'..xxxxx..',
		'.xx.x.xx.',
		'x.xxxxx.x',
		'xxxxxxxxx',
		'x.xxxxx.x',
		'.xxxxxxx.',
		'..xx.xx..'
	],
	'git-branch': [
		'..x...x..',
		'..x...x..',
		'..x...x..',
		'..x...x..',
		'..x.xxx..',
		'..xx.....',
		'..x......',
		'..x......'
	],
	'github': [
		'..xxxxx..',
		'.xx...xx.',
		'x.......x',
		'x.......x',
		'x.......x',
		'.x.....x.',
		'..xx..xx.',
		'..x....x.'
	],
	'monitor': [
		'xxxxxxxxx',
		'x.......x',
		'x.......x',
		'x.......x',
		'xxxxxxxxx',
		'....x....',
		'..xxxxx..'
	],
	'paperclip': [
		'...xx....',
		'..x.xx...',
		'..x..x...',
		'..x..x...',
		'.x...x...',
		'.x...x...',
		'..x.xx...',
		'...xx....'
	],
	'key': [
		'...xxx.xx',
		'..x...xxx',
		'..x...xxx',
		'...xxx.xx',
		'.........',
		'.xxxxxxx.',
		'.x...x...',
		'.xxxxxx..'
	],
	'bell': [
		'....x....',
		'...xxx...',
		'..xxxxx..',
		'.xxxxxxx.',
		'xxxxxxxxx',
		'xxxxxxxxx',
		'.........',
		'...xxx...'
	],
	'eye': [
		'..xxxxx..',
		'.xx...xx.',
		'xx..x..xx',
		'x.xx.xx.x',
		'xx..x..xx',
		'.xx...xx.',
		'..xxxxx..'
	],
	'eye-off': [
		'..xxxx.x.',
		'.xx..xxx.',
		'xx..x.xx.',
		'xxx.xxx.x',
		'.xx.xxx..',
		'xxxx.xx..',
		'..xx.x...'
	],
	'moon': [
		'..xxx..',
		'.xx....',
		'xx.....',
		'xx.....',
		'xx.....',
		'.xx....',
		'..xxx..'
	],
	'log-out': [
		'xxxxx....',
		'x.....x..',
		'x.....xx.',
		'x.xxxxxxx',
		'x.....xx.',
		'x.....x..',
		'xxxxx....'
	],
	'send': [
		'..xxxxx',
		'.....xx',
		'....x.x',
		'...x..x',
		'..x....',
		'.x.....',
		'x......'
	]
};
