// Korean UI strings (#661). Text is the existing Korean copy, unchanged.
// Keep the same key set as en.js (`npm run check:i18n` fails otherwise).
export default {
	// Relative time (shepherdFormat.timeAgo)
	'time.justNow': '방금',
	'time.minutesAgo': '{count}분 전',
	'time.hoursAgo': '{count}시간 전',
	'time.daysAgo': '{count}일 전',

	// Web Push (push.js)
	'push.unsupported': '이 브라우저는 Web Push를 지원하지 않습니다. HTTPS로 접속했는지 확인해 주세요.',
	'push.iosHomeScreen': 'iOS에서는 홈 화면에 추가한 뒤에만 푸시를 켤 수 있습니다.',
	'push.noVapidKey': '서버 VAPID 공개키를 받지 못했습니다. 데몬을 재시작해 주세요.',
	'push.permissionDenied': '알림 권한이 거부되었습니다.',
	'push.subscribeFailed': '구독 등록에 실패했습니다.',
	'push.testFailed': '테스트 전송에 실패했습니다.',

	// Issue status and type labels (IssueTab, IssueDetailModal, ShepherdIssueBoard)
	'issue.status.todo': '작업전',
	'issue.status.in_progress': '작업중',
	'issue.status.testing': '테스트',
	'issue.status.failed': '실패',
	'issue.status.done': '성공',
	'issue.type.design': '설계',
	'issue.type.feature': '기능',
	'issue.type.bug': '버그'
};
