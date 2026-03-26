/**
 * Wireframe rendering preset definitions.
 * Each preset provides CSS variable overrides for .wf-container.
 * Empty vars = use CSS defaults defined in wireframe.css.
 */
export const wireframePresets = {
	default: {
		label: 'Default',
		vars: {}
	},
	minimal: {
		label: 'Minimal',
		vars: {
			'--wf-border': '#e5e5e5',
			'--wf-border-light': '#f0f0f0',
			'--wf-border-strong': '#ccc',
			'--wf-border-input': '#ddd',
			'--wf-surface-alt': '#fafafa',
			'--wf-surface-dim': '#fdfdfd',
			'--wf-icon-bg': '#eee',
			'--wf-avatar-bg': '#e0e0e0',
			'--wf-radius': '4px'
		}
	},
	blueprint: {
		label: 'Blueprint',
		vars: {
			'--wf-bg': '#1a2744',
			'--wf-text': '#a8c4e0',
			'--wf-text-muted': '#6b8ab0',
			'--wf-text-subtle': '#8aa8cc',
			'--wf-text-link': '#7a9ec0',
			'--wf-border': '#2d4a6f',
			'--wf-border-light': '#243a5c',
			'--wf-border-strong': '#3d6090',
			'--wf-border-input': '#2d4a6f',
			'--wf-surface': '#1e2f4d',
			'--wf-surface-alt': '#243654',
			'--wf-surface-dim': '#1c2a45',
			'--wf-icon-bg': '#2d4a6f',
			'--wf-avatar-bg': '#3d5a80',
			'--wf-badge-bg': '#2d4a6f',
			'--wf-primary': '#5b9bd5',
			'--wf-danger': '#d97755',
			'--wf-success': '#5bb590'
		}
	},
	sketch: {
		label: 'Sketch',
		vars: {
			'--wf-bg': '#fffef5',
			'--wf-text': '#444',
			'--wf-text-muted': '#aaa',
			'--wf-text-subtle': '#777',
			'--wf-text-link': '#888',
			'--wf-border': '#bbb',
			'--wf-border-light': '#d5d5c8',
			'--wf-border-strong': '#999',
			'--wf-border-input': '#bbb',
			'--wf-surface': '#fffef5',
			'--wf-surface-alt': '#f5f0e0',
			'--wf-surface-dim': '#faf5e8',
			'--wf-icon-bg': '#e8e0c8',
			'--wf-avatar-bg': '#ddd5bb',
			'--wf-badge-bg': '#e8e0c8',
			'--wf-radius': '2px',
			'--wf-font-size': '13px'
		}
	},
	dark: {
		label: 'Dark',
		vars: {
			'--wf-bg': '#1e1e1e',
			'--wf-text': '#d4d4d4',
			'--wf-text-muted': '#777',
			'--wf-text-subtle': '#aaa',
			'--wf-text-link': '#999',
			'--wf-border': '#444',
			'--wf-border-light': '#333',
			'--wf-border-strong': '#666',
			'--wf-border-input': '#555',
			'--wf-surface': '#252525',
			'--wf-surface-alt': '#2a2a2a',
			'--wf-surface-dim': '#222',
			'--wf-icon-bg': '#444',
			'--wf-avatar-bg': '#555',
			'--wf-badge-bg': '#444',
			'--wf-primary': '#5b9bd5',
			'--wf-danger': '#e06050',
			'--wf-success': '#4caf70'
		}
	}
};

/** All CSS variable keys used across presets */
export const allVarKeys = [
	'--wf-bg', '--wf-text', '--wf-text-muted', '--wf-text-subtle', '--wf-text-link',
	'--wf-border', '--wf-border-light', '--wf-border-strong', '--wf-border-input',
	'--wf-surface', '--wf-surface-alt', '--wf-surface-dim',
	'--wf-icon-bg', '--wf-avatar-bg', '--wf-badge-bg',
	'--wf-primary', '--wf-danger', '--wf-success',
	'--wf-radius', '--wf-font-size'
];
