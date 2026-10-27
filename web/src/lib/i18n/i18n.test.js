/**
 * i18n module tests (#662). Node built-in runner:
 *   node --test web/src/lib/i18n/i18n.test.js
 *
 * The dictionaries are plain objects, so a test can add or remove one key at
 * runtime to exercise the fallback and plural paths. Every change is undone in
 * a finally block; the files on disk never change (npm run check:i18n keeps
 * checking that ko.js and en.js define the same keys).
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { get } from 'svelte/store';
import en from './en.js';
import ko from './ko.js';
import {
	locale,
	setLocale,
	normalizeLocale,
	detectLocale,
	translate,
	t,
	enumLabel
} from './index.js';
import { timeAgo } from '../shepherdFormat.js';

describe('normalizeLocale / detectLocale', () => {
	it('accepts a language tag and reports the supported base', () => {
		assert.equal(normalizeLocale('ko'), 'ko');
		assert.equal(normalizeLocale('EN'), 'en');
		assert.equal(normalizeLocale('ko-KR'), 'ko');
		assert.equal(normalizeLocale('en_US'), 'en');
	});

	it('rejects unsupported or empty values', () => {
		assert.equal(normalizeLocale('de'), null);
		assert.equal(normalizeLocale(''), null);
		assert.equal(normalizeLocale(undefined), null);
	});

	it('picks ko for a Korean browser and en otherwise', () => {
		assert.equal(detectLocale('ko-KR'), 'ko');
		assert.equal(detectLocale('en-US'), 'en');
		assert.equal(detectLocale('ja'), 'en');
		assert.equal(detectLocale(''), 'en');
	});
});

describe('translate', () => {
	it('returns the text of the current locale', () => {
		setLocale('ko');
		assert.equal(translate('issue.status.todo'), '작업전');
		setLocale('en');
		assert.equal(translate('issue.status.todo'), 'To do');
		assert.equal(get(locale), 'en');
	});

	it('falls back to en when the key is missing from ko', () => {
		setLocale('ko');
		assert.ok('time.justNow' in ko, 'ko.js must define time.justNow');
		const saved = ko['time.justNow'];
		delete ko['time.justNow'];
		try {
			assert.equal(translate('time.justNow'), en['time.justNow']);
		} finally {
			ko['time.justNow'] = saved;
		}
		assert.equal(translate('time.justNow'), '방금');
	});

	it('returns the key when no dictionary has it', () => {
		setLocale('en');
		assert.equal(translate('no.such.key'), 'no.such.key');
		setLocale('ko');
		assert.equal(translate('no.such.key'), 'no.such.key');
	});

	it('replaces {name} placeholders with params', () => {
		setLocale('ko');
		assert.equal(translate('time.minutesAgo', { count: 5 }), '5분 전');
		setLocale('en');
		assert.equal(translate('time.hoursAgo', { count: 3 }), '3h ago');
	});

	it('leaves placeholders with no value in params as they are', () => {
		setLocale('ko');
		assert.equal(translate('time.minutesAgo'), '{count}분 전');
		assert.equal(translate('time.minutesAgo', {}), '{count}분 전');
		assert.equal(translate('time.minutesAgo', { other: 1 }), '{count}분 전');
	});

	it('uses the .one/.other plural form chosen by count', () => {
		// The documented plural mechanism (see index.js): define `<key>.one`
		// and `<key>.other`, pass `count` in params.
		en['task.count.one'] = '{count} task';
		en['task.count.other'] = '{count} tasks';
		try {
			setLocale('en');
			assert.equal(translate('task.count', { count: 1 }), '1 task');
			assert.equal(translate('task.count', { count: 4 }), '4 tasks');
			assert.equal(translate('task.count', { count: 0 }), '0 tasks');
		} finally {
			delete en['task.count.one'];
			delete en['task.count.other'];
		}
	});

	it('ignores an unsupported value in setLocale', () => {
		setLocale('ko');
		assert.equal(setLocale('de'), null);
		assert.equal(get(locale), 'ko');
		assert.equal(setLocale('en'), 'en');
	});
});

describe('t store', () => {
	it('follows the locale without a reload', () => {
		setLocale('ko');
		assert.equal(get(t)('issue.status.in_progress'), '작업중');
		setLocale('en');
		assert.equal(get(t)('issue.status.in_progress'), 'In progress');
	});

	it('interpolates through the store too', () => {
		setLocale('en');
		assert.equal(get(t)('time.daysAgo', { count: 2 }), '2d ago');
	});
});

describe('enumLabel', () => {
	it('labels a known enum value in the current locale', () => {
		setLocale('ko');
		assert.equal(enumLabel(get(t), 'issue.status', 'done'), '성공');
		setLocale('en');
		assert.equal(enumLabel(get(t), 'issue.type', 'bug'), 'Bug');
	});

	it('shows an unknown value as itself instead of a raw key', () => {
		setLocale('en');
		assert.equal(enumLabel(get(t), 'issue.status', 'archived'), 'archived');
		assert.equal(enumLabel(get(t), 'issue.status', ''), '');
		assert.equal(enumLabel(get(t), 'issue.status', null), '');
	});
});

describe('timeAgo (shepherdFormat) follows the locale', () => {
	// The API sends local wall-clock strings ("2026-10-27 22:20:06") and
	// timeAgo parses them as local time, so the stamp is built in that shape.
	const stamp = (secondsAgo) => {
		const d = new Date(Date.now() - secondsAgo * 1000); // truncated to the second, in the past
		const p = (n) => String(n).padStart(2, '0');
		return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
	};

	it('prints Korean relative time for ko', () => {
		setLocale('ko');
		assert.equal(timeAgo(stamp(30)), '방금');
		assert.equal(timeAgo(stamp(5 * 60)), '5분 전');
		assert.equal(timeAgo(stamp(3 * 3600)), '3시간 전');
		assert.equal(timeAgo(stamp(2 * 86400)), '2일 전');
	});

	it('prints English relative time for en', () => {
		setLocale('en');
		assert.equal(timeAgo(stamp(30)), 'just now');
		assert.equal(timeAgo(stamp(5 * 60)), '5m ago');
		assert.equal(timeAgo(stamp(3 * 3600)), '3h ago');
		assert.equal(timeAgo(stamp(2 * 86400)), '2d ago');
	});

	it('keeps the same language when called again after a locale change', () => {
		setLocale('en');
		const value = stamp(7 * 60);
		assert.equal(timeAgo(value), '7m ago');
		setLocale('ko');
		assert.equal(timeAgo(value), '7분 전');
	});

	it('returns an empty string for missing or invalid input', () => {
		setLocale('en');
		assert.equal(timeAgo(''), '');
		assert.equal(timeAgo(null), '');
		assert.equal(timeAgo(undefined), '');
		assert.equal(timeAgo('not-a-date'), '');
	});
});

describe('push.js messages are translatable', () => {
	it('has text for every push key in both dictionaries', () => {
		for (const key of [
			'push.unsupported',
			'push.iosHomeScreen',
			'push.noVapidKey',
			'push.permissionDenied',
			'push.subscribeFailed',
			'push.testFailed'
		]) {
			assert.equal(typeof en[key], 'string', `en.js: ${key}`);
			assert.equal(typeof ko[key], 'string', `ko.js: ${key}`);
		}
	});
});

describe('dictionary key sets', () => {
	it('ko.js and en.js define the same keys (check:i18n also enforces this)', () => {
		const koKeys = Object.keys(ko).sort();
		const enKeys = Object.keys(en).sort();
		assert.deepEqual(koKeys, enKeys);
	});

	it('issue status and type keys cover every value used by the board', () => {
		for (const status of ['todo', 'in_progress', 'testing', 'failed', 'done']) {
			assert.equal(typeof en[`issue.status.${status}`], 'string');
			assert.equal(typeof ko[`issue.status.${status}`], 'string');
		}
		for (const type of ['design', 'feature', 'bug']) {
			assert.equal(typeof en[`issue.type.${type}`], 'string');
			assert.equal(typeof ko[`issue.type.${type}`], 'string');
		}
	});

	it('every relative-time key uses the same {count} placeholder in both languages', () => {
		for (const key of ['time.minutesAgo', 'time.hoursAgo', 'time.daysAgo']) {
			assert.match(en[key], /\{count\}/, key);
			assert.match(ko[key], /\{count\}/, key);
		}
	});
});
