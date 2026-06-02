import { authedFetch, parseJsonData, withApiResult } from './core.js';

export const fetchPublicSettings = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/settings/public', { suppressUnauthorizedEvent: true });
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取公开设置失败:' });
};

export const fetchSettings = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/settings/get');
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取设置失败:' });
};

export const updateSettings = async (payload = {}) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/settings/update', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: JSON.stringify(payload || {}),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 更新设置失败:' });
};

export const restartController = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/settings/restart-controller', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: '{}',
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 重启管理进程失败:' });
};
