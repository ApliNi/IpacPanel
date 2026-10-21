import { sseClient } from './sseClient.js';

const listeners = new Set();
const readyWaiters = new Set();

const storeState = {
	instances: [],
	version: 0,
	started: false,
	ready: false,
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
	emit();
};

const applyPatch = (payload) => {
	const version = Number.isFinite(payload?.version) ? Number(payload.version) : 0;
	const items = Array.isArray(payload?.items) ? payload.items : [];
	if (version <= 0 || (storeState.version > 0 && version !== storeState.version + 1)) {
		sseClient.reconnect();
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
		sseClient.reconnect();
		return;
	}
	emit(changedNames);
};

sseClient.on('snapshot', (payload) => {
	if (!storeState.started) return;
	try {
		replaceSnapshot(payload);
	} catch (error) {
		console.error('[SSE] 解析实例全量事件失败:', error);
	}
});

sseClient.on('patch', (payload) => {
	if (!storeState.started) return;
	try {
		applyPatch(payload);
	} catch (error) {
		console.error('[SSE] 解析实例增量事件失败:', error);
	}
});

sseClient.on('auth_required', () => {
	if (!storeState.started) return;
	rejectReadyWaiters(new Error('需要身份验证'));
	stop();
});

export const start = () => {
	if (storeState.started) return;
	storeState.started = true;
	sseClient.start();
	if (sseClient.isConnected() && storeState.version === 0) {
		sseClient.reconnect(0);
	}
};

export const stop = () => {
	if (!storeState.started) return;
	storeState.started = false;
	sseClient.stop();
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

export default instanceStatusStore;
