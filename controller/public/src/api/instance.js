import {
	authedFetch,
	createApiFailure,
	createApiSuccess,
	createHttpError,
	getCSRFToken,
	parseJsonData,
	parseJsonSafe,
	parseJsonPayload,
	withApiResult,
} from './core.js';

export const controlInstance = async (name, action) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/instance/control', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ name, action })
		});
		const payload = await parseJsonPayload(res);
		if (payload && payload.ok === false) {
			throw new Error(payload.message || `instance ${action} failed`);
		}
		return payload?.data || { ok: true };
	}, { logMessage: `[API] 对实例 ${name} 执行 ${action} 操作失败:` });
};

export const createInstance = async (payload) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/instance/create', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(payload)
		});
		return await parseJsonData(res);
	}, {
		logMessage: '[API] 创建实例失败:',
	});
};

export const fetchInstance = async (name) => {
	return await withApiResult(async () => {
		const instanceName = String(name || '').trim();
		if (!instanceName) {
			throw new Error('missing instance name');
		}
		const res = await authedFetch(`/api/instance/get?name=${encodeURIComponent(instanceName)}`);
		return await parseJsonData(res);
	}, { logMessage: `[API] 获取实例 ${name} 详情失败:` });
};

export const updateInstance = async (name, payload) => {
	return await withApiResult(async () => {
		const res = await authedFetch(`/api/instance/update?name=${encodeURIComponent(name)}`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(payload)
		});
		return await parseJsonData(res);
	}, {
		logMessage: `[API] 更新实例 ${name} 失败:`,
	});
};

export const deleteInstance = async (name, options = {}) => {
	try {
		const res = await authedFetch('/api/instance/delete', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({
				name,
				delete_files: options.deleteFiles === true,
				confirm_shared_delete: options.confirmSharedDelete === true,
			}),
		});
		const payload = await parseJsonSafe(res);
		if (!res.ok) {
			if (payload?.message) {
				return createApiFailure(new Error(payload.message), {
					confirmRequired: payload?.data?.confirm_required === true,
				});
			}
			return createApiFailure(await createHttpError(res), {
				confirmRequired: payload?.data?.confirm_required === true,
			});
		}
		if (payload?.ok === false) {
			return createApiFailure(new Error(payload.message || '删除实例失败'));
		}
		return createApiSuccess(payload?.data || { ok: true });
	} catch (error) {
		console.error('[API] deleteInstance 失败:', error);
		return createApiFailure(error);
	}
};

export const updateGroup = async (from, to) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/group/update', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ from, to })
		});
		return await parseJsonData(res);
	}, {
		logMessage: '[API] updateGroup 失败:',
	});
};

export const createWebSocket = (name, { onOpen, onMessage, onClose }) => {
	const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
	const query = new URLSearchParams({ name });
	const csrfToken = getCSRFToken();
	if (csrfToken) {
		query.set('csrf', csrfToken);
	}
	const wsUrl = `${protocol}//${window.location.host}/api/instance/ws?${query.toString()}`;
	const socket = new WebSocket(wsUrl);
	socket.binaryType = 'arraybuffer';

	socket.onopen = onOpen;
	socket.onmessage = onMessage;
	socket.onclose = onClose;

	return socket;
};
