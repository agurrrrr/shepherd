// English UI strings (#661). en is the base locale: a key missing from ko.js
// falls back to the text here. Keys are `area.item`; see index.js for the
// placeholder and plural rules. Keep the same key set as ko.js
// (`npm run check:i18n` fails otherwise).
export default {
	// Relative time (shepherdFormat.timeAgo) — same short style as the dashboard.
	'time.justNow': 'just now',
	'time.minutesAgo': '{count}m ago',
	'time.hoursAgo': '{count}h ago',
	'time.daysAgo': '{count}d ago',

	// Web Push (push.js)
	'push.unsupported': 'This browser does not support Web Push. Make sure you opened Shepherd over HTTPS.',
	'push.iosHomeScreen': 'On iOS, add Shepherd to the Home Screen before you turn on push.',
	'push.noVapidKey': 'Could not get the server VAPID public key. Restart the daemon.',
	'push.permissionDenied': 'Notification permission was denied.',
	'push.subscribeFailed': 'Failed to register the push subscription.',
	'push.testFailed': 'Failed to send the test notification.',

	// Issue status and type labels (IssueTab, IssueDetailModal, ShepherdIssueBoard)
	'issue.status.todo': 'To do',
	'issue.status.in_progress': 'In progress',
	'issue.status.testing': 'Testing',
	'issue.status.failed': 'Failed',
	'issue.status.done': 'Done',
	'issue.type.design': 'Design',
	'issue.type.feature': 'Feature',
	'issue.type.bug': 'Bug'
};
