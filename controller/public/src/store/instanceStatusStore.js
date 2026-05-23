import { dispatchUnauthorized, parseSSEJsonData, postEventStream, readSSEStream } from '../api/core.js';

const RECONNECT_BASE_DELAY_MS = 400;
const RECONNECT_MAX_DELAY_MS = 5000;
const RECONNECT_JITTER_RATIO = 0.2;

const listeners = new Set();
const readyWaiters = new Set();

const storeState = {
	instances: [],
	version: 0,
	stream: null,
	reconnectTimer: null,
	reconnectAttempt: 0,
	started: false,
	ready: false,
};

const getReconnectDelay = (attempt) => {
	const safeAttempt = Math.max(0, Number(attempt) || 0);
	const rawDelay = Math.min(RECONNECT_MAX_DELAY_MS, RECONNECT_BASE_DELAY_MS * (2 ** Math.max(0, safeAttempt - 1)));
	const jitterFactor = 1 + ((Math.random() * 2 - 1) * RECONNECT_JITTER_RATIO);
	return Math.max(RECONNECT_BASE_DELAY_MS, Math.round(rawDelay * jitterFactor));
};

const cloneInstances = (instances) => Array.isArray(instances)
	? instances.map((item) => ({ ...item }))
	: [];

const emit = (changedNames = null) => {
	const snapshot = getSnapshot();
	if (snapshot.ready) {
		readyWaiters.forEach((waiter) => waiter.resolve(snapshot));
		readyWaiters.clear();
	}
	listeners.forEach((listener) => {
		try {
			listener(snapshot, changedNames);
		} catch (error) {
			console.error('[SSE] 实例状态订阅回调失败:', error);
		}
	});
};

const rejectReadyWaiters = (error) => {
	readyWaiters.forEach((waiter) => waiter.reject(error));
	readyWaiters.clear();
};

export const getSnapshot = () => ({
	instances: cloneInstances(storeState.instances),
	version: storeState.version,
	ready: storeState.ready,
});

export const getInstances = () => cloneInstances(storeState.instances);

export const getInstance = (name) => {
	const target = String(name || '').trim();
	if (!target) return null;
	const found = storeState.instances.find((item) => String(item?.name || '') === target);
	return found ? { ...found } : null;
};

export const subscribe = (listener) => {
	if (typeof listener !== 'function') {
		return () => {};
	}
	listeners.add(listener);
	listener(getSnapshot());
	return () => listeners.delete(listener);
};

export const waitForReady = () => {
	if (storeState.ready) {
		return Promise.resolve(getSnapshot());
	}
	start();
	return new Promise((resolve, reject) => {
		readyWaiters.add({ resolve, reject });
	});
};

const replaceSnapshot = (payload) => {
	storeState.instances = cloneInstances(payload?.items);
	storeState.version = Number.isFinite(payload?.version) ? Number(payload.version) : 0;
	storeState.ready = true;
	storeState.reconnectAttempt = 0;
	emit();
};

const applyPatch = (payload) => {
	const version = Number.isFinite(payload?.version) ? Number(payload.version) : 0;
	const items = Array.isArray(payload?.items) ? payload.items : [];
	if (version <= 0 || (storeState.version > 0 && version !== storeState.version + 1)) {
		stopSource();
		scheduleReconnect(0);
		return;
	}
	const patchMap = new Map();
	for (const item of items) {
		const name = String(item?.name || '').trim();
		if (name) patchMap.set(name, item);
	}
	if (patchMap.size === 0) {
		storeState.version = version;
		return;
	}
	const changedNames = new Set();
	let requiresFull = false;
	storeState.instances = storeState.instances.map((item) => {
		const name = String(item?.name || '').trim();
		const patch = patchMap.get(name);
		if (!patch) return item;
		patchMap.delete(name);
		changedNames.add(name);
		return { ...item, ...patch };
	});
	if (patchMap.size > 0) {
		requiresFull = true;
	}
	storeState.version = version;
	if (requiresFull) {
		stopSource();
		scheduleReconnect(0);
		return;
	}
	emit(changedNames);
};

const stopSource = () => {
	if (!storeState.stream) return;
	storeState.stream.controller.abort();
	storeState.stream = null;
};

const scheduleReconnect = (delay = null) => {
	if (!storeState.started || storeState.reconnectTimer) return;
	storeState.reconnectAttempt += 1;
	const reconnectDelay = delay ?? getReconnectDelay(storeState.reconnectAttempt);
	storeState.reconnectTimer = setTimeout(() => {
		storeState.reconnectTimer = null;
		connect();
	}, reconnectDelay);
};

const connect = async () => {
	if (!storeState.started || storeState.stream) return;
	const controller = new AbortController();
	const stream = { controller };
	storeState.stream = stream;
	try {
		const res = await postEventStream('/api/instance/events', {}, { signal: controller.signal });
		await readSSEStream(res, {
			auth_required() {
				rejectReadyWaiters(new Error('需要身份验证'));
				stop();
				dispatchUnauthorized();
			},
			instances_full(event) {
				try {
					replaceSnapshot(parseSSEJsonData(event, '实例全量事件解析失败'));
				} catch (error) {
					console.error('[SSE] 解析实例全量事件失败:', error);
				}
			},
			instances_patch(event) {
				try {
					applyPatch(parseSSEJsonData(event, '实例增量事件解析失败'));
				} catch (error) {
					console.error('[SSE] 解析实例增量事件失败:', error);
				}
			},
		});
	} catch (error) {
		if (error.name !== 'AbortError') {
			console.error('[SSE] 实例状态流连接失败:', error);
		}
	} finally {
		if (storeState.stream === stream) {
			storeState.stream = null;
			if (storeState.started) {
				scheduleReconnect();
			}
		}
	}
};

export const start = () => {
	storeState.started = true;
	if (storeState.reconnectTimer) {
		clearTimeout(storeState.reconnectTimer);
		storeState.reconnectTimer = null;
	}
	connect();
};

export const stop = () => {
	storeState.started = false;
	if (storeState.reconnectTimer) {
		clearTimeout(storeState.reconnectTimer);
		storeState.reconnectTimer = null;
	}
	stopSource();
	storeState.ready = false;
	storeState.version = 0;
	storeState.instances = [];
	rejectReadyWaiters(new Error('实例状态流已停止'));
	emit();
};

export const instanceStatusStore = {
	start,
	stop,
	subscribe,
	getSnapshot,
	getInstances,
	getInstance,
	waitForReady,
};
