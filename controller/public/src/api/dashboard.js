import { authedFetch, postEventStream, parseJsonData, withApiResult } from './core.js';

const normalizeMinutes = (minutes) => {
	const value = Number.parseInt(String(minutes), 10);
	if (!Number.isFinite(value) || value <= 0) {
		return 30;
	}
	return Math.min(value, 10080);
};

const buildDashboardPayload = ({ minutes = 30, nic = '', disk = '' } = {}) => ({
	minutes: normalizeMinutes(minutes),
	nic: String(nic || '').trim(),
	disk: String(disk || '').trim(),
});

export const fetchDashboardSnapshot = async ({ minutes = 30, nic = '', disk = '' } = {}, options = {}) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/dashboard/snapshot', {
			method: 'POST',
			suppressUnauthorizedEvent: options.suppressUnauthorizedEvent === true,
			headers: {
				'Content-Type': 'application/json',
			},
			body: JSON.stringify(buildDashboardPayload({ minutes, nic, disk })),
		});
		return await parseJsonData(res);
	}, { logMessage: '[Dashboard API] 获取仪表板快照失败:' });
};

export const openDashboardEventStream = async ({ minutes = 30, nic = '', disk = '' } = {}, options = {}) => {
	return await postEventStream('/api/dashboard/events', buildDashboardPayload({ minutes, nic, disk }), options);
};
