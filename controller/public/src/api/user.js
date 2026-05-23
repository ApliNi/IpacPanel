import { authedFetch, parseJsonData, withApiResult } from './core.js';

export const fetchAdminUser = async (username) => {
	return await withApiResult(async () => {
		const u = String(username || '').trim();
		if (!u) {
			throw new Error('缺少用户');
		}
		const res = await authedFetch(`/api/admin/get?user=${encodeURIComponent(u)}`);
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取用户详情失败:' });
};

export const updateAdminUser = async (payload) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/admin/update', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: JSON.stringify(payload || {}),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 更新用户失败:' });
};

export const createAdminUser = async (payload) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/admin/create', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: JSON.stringify(payload || {}),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 创建用户失败:' });
};

export const deleteAdminUser = async (username) => {
	return await withApiResult(async () => {
		const user = String(username || '').trim();
		if (!user) {
			throw new Error('缺少用户');
		}
		const res = await authedFetch('/api/admin/delete', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: JSON.stringify({ user }),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 删除用户失败:' });
};

export const fetchUsers = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/user/list');
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取用户列表失败:' });
};

export const fetchMe = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/user/get', { skipUnauthorizedReload: true });
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取当前用户信息失败:' });
};

export const resolveBootAuthState = async () => {
	try {
		const res = await authedFetch('/api/user/get', { skipUnauthorizedReload: true });
		if (res.status === 401) {
			return {
				status: 'unauthorized',
				user: null,
				error: null,
			};
		}
		if (!res.ok) {
			throw new Error(await res.text().catch(() => '') || `HTTP ${res.status}`);
		}
		const user = await parseJsonData(res);
		if (!user) {
			throw new Error('当前用户载荷为空');
		}
		return {
			status: 'authenticated',
			user,
			error: null,
		};
	} catch (error) {
		return {
			status: 'unavailable',
			user: null,
			error,
		};
	}
};

export const updateMe = async ({ name = '', pass = '' } = {}) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/user/update', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: JSON.stringify({ name, pass }),
		});
		return await parseJsonData(res);
	}, { logMessage: '[API] 更新当前用户失败:' });
};
