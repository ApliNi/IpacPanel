import { postEventStream } from './core.js';

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

export const openDashboardEventStream = async ({ minutes = 30, nic = '', disk = '' } = {}, options = {}) => {
	return await postEventStream('/api/dashboard/events', buildDashboardPayload({ minutes, nic, disk }), options);
};
