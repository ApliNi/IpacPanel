import { dispatchUnauthorized, parseSSEJsonData, postEventStream, readSSEStream } from '../api/core.js';

const RECONNECT_BASE_DELAY_MS = 400;
const RECONNECT_MAX_DELAY_MS = 5000;
const RECONNECT_JITTER_RATIO = 0.2;

let refCount = 0;
let stream = null;
let reconnectTimer = null;
let reconnectAttempt = 0;
let connected = false;

const listeners = new Map();

const getReconnectDelay = (attempt) => {
	const safeAttempt = Math.max(0, Number(attempt) || 0);
	const rawDelay = Math.min(RECONNECT_MAX_DELAY_MS, RECONNECT_BASE_DELAY_MS * (2 ** Math.max(0, safeAttempt - 1)));
	const jitterFactor = 1 + ((Math.random() * 2 - 1) * RECONNECT_JITTER_RATIO);
	return Math.max(RECONNECT_BASE_DELAY_MS, Math.round(rawDelay * jitterFactor));
};

const emit = (eventType, payload, rawEvent = null) => {
	const handlers = listeners.get(eventType);
	if (!handlers || handlers.size === 0) return;
	for (const handler of Array.from(handlers)) {
		try {
			handler(payload, rawEvent);
		} catch (error) {
			console.error(`[SSE] 处理 ${eventType} 事件失败:`, error);
		}
	}
};

export const on = (eventType, handler) => {
	const type = String(eventType || '').trim();
	if (!type || typeof handler !== 'function') {
		return () => {};
	}
	if (!listeners.has(type)) {
		listeners.set(type, new Set());
	}
	listeners.get(type).add(handler);
	return () => off(type, handler);
};

export const off = (eventType, handler) => {
	const type = String(eventType || '').trim();
	const set = listeners.get(type);
	if (!set) return;
	set.delete(handler);
	if (set.size === 0) {
		listeners.delete(type);
	}
};

export const subscribe = (handlersOrType, optionalHandler) => {
	if (typeof handlersOrType === 'string' && typeof optionalHandler === 'function') {
		return on(handlersOrType, optionalHandler);
	}
	if (handlersOrType && typeof handlersOrType === 'object') {
		const unsubs = [];
		for (const [type, handler] of Object.entries(handlersOrType)) {
			if (typeof handler === 'function') {
				unsubs.push(on(type, handler));
			}
		}
		return () => {
			for (const unsub of unsubs) {
				unsub();
			}
		};
	}
	return () => {};
};

const stopSource = () => {
	if (!stream) return;
	const current = stream;
	stream = null;
	current.controller.abort();
};

const scheduleReconnect = (delay = null) => {
	if (refCount <= 0 || reconnectTimer) return;
	reconnectAttempt += 1;
	const reconnectDelay = delay ?? getReconnectDelay(reconnectAttempt);
	reconnectTimer = setTimeout(() => {
		reconnectTimer = null;
		connect();
	}, reconnectDelay);
};

const connect = async () => {
	if (refCount <= 0 || stream) return;
	const controller = new AbortController();
	const currentStream = { controller };
	stream = currentStream;

	try {
		const res = await postEventStream('/api/instance/events', {}, { signal: controller.signal });
		connected = true;
		reconnectAttempt = 0;
		emit('connect', { res }, null);

		await readSSEStream(res, {
			auth_required(event) {
				emit('auth_required', null, event);
				stopAll();
				dispatchUnauthorized();
			},
			instances_full(event) {
				try {
					const payload = parseSSEJsonData(event, '实例全量事件解析失败');
					emit('snapshot', payload, event);
					emit('instances_full', payload, event);
				} catch (error) {
					console.error('[SSE] 解析实例全量事件失败:', error);
				}
			},
			instances_patch(event) {
				try {
					const payload = parseSSEJsonData(event, '实例增量事件解析失败');
					emit('patch', payload, event);
					emit('instances_patch', payload, event);
				} catch (error) {
					console.error('[SSE] 解析实例增量事件失败:', error);
				}
			},
			log_count(event) {
				try {
					const payload = parseSSEJsonData(event, '运行日志计数事件解析失败');
					emit('log_count', payload, event);
				} catch (error) {
					console.error('[SSE] 解析运行日志计数事件失败:', error);
				}
			},
			log_entry(event) {
				try {
					const payload = parseSSEJsonData(event, '运行日志条目事件解析失败');
					emit('log_entry', payload, event);
				} catch (error) {
					console.error('[SSE] 解析运行日志条目事件失败:', error);
				}
			},
			message(event) {
				if (event.type === 'instances_full' || event.type === 'instances_patch' || event.type === 'log_count' || event.type === 'log_entry') {
					return;
				}
				try {
					const payload = parseSSEJsonData(event, '事件数据解析失败');
					emit(event.type, payload, event);
				} catch {
					emit(event.type, event.data, event);
				}
			},
		});
	} catch (error) {
		if (error.name !== 'AbortError') {
			console.error('[SSE] 事件流连接失败:', error);
			emit('error', error, null);
			if (error?.status === 401 || error?.message === 'HTTP 401' || error?.message?.includes('401')) {
				stopAll();
				dispatchUnauthorized();
				return;
			}
		}
	} finally {
		connected = false;
		if (stream === currentStream) {
			stream = null;
			emit('disconnect', null, null);
			if (refCount > 0) {
				scheduleReconnect();
			}
		}
	}
};

const stopAll = () => {
	refCount = 0;
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	stopSource();
};

export const start = () => {
	refCount += 1;
	if (refCount === 1) {
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
		}
		connect();
	}
	return () => stop();
};

export const stop = () => {
	if (refCount > 0) {
		refCount -= 1;
	}
	if (refCount === 0) {
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
		}
		stopSource();
	}
};

export const reconnect = (delay = null) => {
	if (refCount <= 0) return;
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	stopSource();
	if (delay !== null && delay <= 0) {
		connect();
	} else {
		scheduleReconnect(delay);
	}
};

export const isConnected = () => connected;
export const getRefCount = () => refCount;

export const sseClient = {
	start,
	stop,
	reconnect,
	on,
	off,
	subscribe,
	isConnected,
	getRefCount,
};

export default sseClient;
