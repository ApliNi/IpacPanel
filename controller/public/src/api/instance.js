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
			body: JSON.stringify({ instance: name, action })
		});
		const payload = await parseJsonPayload(res);
		if (payload && payload.ok === false) {
			throw new Error(payload.message || `实例 ${action} 失败`);
		}
		return payload?.data || { ok: true };
	}, { logMessage: `[API] 对实例 ${name} 执行 ${action} 操作失败:` });
};

const toInstanceConfigPayload = (payload = {}) => {
	const config = Object.assign({}, payload || {});
	if (Object.prototype.hasOwnProperty.call(config, 'name')) {
		config.instance = config.name;
		delete config.name;
	}
	return config;
};

export const createInstance = async (payload) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/instance/create', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(toInstanceConfigPayload(payload))
		});
		return await parseJsonData(res);
	}, {
		logMessage: '[API] 创建实例失败:',
	});
};

export const fetchInstance = async (name) => {
	return await withApiResult(async () => {
		const instanceName = String(name || '');
		if (!instanceName) {
			throw new Error('缺少实例名称');
		}
		const res = await authedFetch('/api/instance/get', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ instance: instanceName })
		});
		return await parseJsonData(res);
	}, { logMessage: `[API] 获取实例 ${name} 详情失败:` });
};

export const updateInstance = async (name, payload) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/instance/update', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ instance: name, config: toInstanceConfigPayload(payload) })
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
				instance: name,
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

const encodeTerminalProtocolPayload = (payload) => {
	try {
		const json = JSON.stringify(payload);
		if (typeof json !== 'string') {
			throw new Error('终端连接参数序列化失败');
		}

		const bytes = new TextEncoder().encode(json);
		let binary = '';
		for (const byte of bytes) {
			binary += String.fromCharCode(byte);
		}

		return btoa(binary)
			.replace(/\+/g, '-')
			.replace(/\//g, '_')
			.replace(/=+$/g, '');
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`终端连接参数编码失败: ${message}`);
	}
};

const createTerminalProtocol = (name) => {
	const instance = String(name || '');
	if (!instance) {
		throw new Error('缺少实例名称');
	}

	const csrf = getCSRFToken();
	if (!csrf) {
		throw new Error('缺少 CSRF Token');
	}

	return encodeTerminalProtocolPayload({ instance, csrf });
};

export const createWebSocket = (name, { onOpen, onMessage, onClose }) => {
	const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
	const terminalProtocol = createTerminalProtocol(name);
	const wsUrl = `${protocol}//${window.location.host}/api/instance/ws`;
	const socket = new WebSocket(wsUrl, terminalProtocol);
	socket.binaryType = 'arraybuffer';

	socket.onopen = onOpen;
	socket.onmessage = onMessage;
	socket.onclose = onClose;

	return socket;
};
