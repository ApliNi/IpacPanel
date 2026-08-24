import { authedFetch, parseJsonData, withApiResult } from './core.js';

export const fetchLogs = async ({ beforeSeq = 0, limit = 100, instance = '', levels = [] } = {}) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/log/get', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				before_seq: Math.max(0, Number(beforeSeq) || 0),
				limit: Math.max(1, Number(limit) || 1),
				instance: String(instance || '').trim(),
				levels: Array.isArray(levels) ? levels : [],
			}),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取运行日志失败:' });
};

export const clearLogs = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/log/clear', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 清空运行日志失败:' });
};
