import { authedFetch, getCSRFToken, parseJsonData, withApiResult } from './core.js';

const normalizeMinutes = (minutes) => {
	const value = Number.parseInt(String(minutes), 10);
	if (!Number.isFinite(value) || value <= 0) {
		return 30;
	}
	return Math.min(value, 10080);
};

const applyDashboardQueryParams = (params, { minutes = 30, nic = '', disk = '' } = {}) => {
	params.set('minutes', String(normalizeMinutes(minutes)));
	params.set('nic', String(nic || '').trim());
	params.set('disk', String(disk || '').trim());
};

export const fetchDashboardSnapshot = async ({ minutes = 30, nic = '', disk = '' } = {}) => {
	const params = new URLSearchParams();
	applyDashboardQueryParams(params, { minutes, nic, disk });
	return await withApiResult(async () => {
		const res = await authedFetch(`/api/dashboard/snapshot?${params.toString()}`);
		return await parseJsonData(res);
	}, { logMessage: '[Dashboard API] 获取仪表板快照失败:' });
};

export const buildDashboardEventsURL = ({ minutes = 30, nic = '', disk = '' } = {}) => {
	const params = new URLSearchParams();
	applyDashboardQueryParams(params, { minutes, nic, disk });
	const csrfToken = getCSRFToken();
	if (csrfToken) {
		params.set('csrf', csrfToken);
	}
	return `/api/dashboard/events?${params.toString()}`;
};
