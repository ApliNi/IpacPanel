import { dispatchUnauthorized, parseSSEJsonData, postEventStream, readSSEStream } from '../api/core.js';
import { fetchLogs } from '../api/log.js';

const RECONNECT_BASE_DELAY_MS = 400;
const RECONNECT_MAX_DELAY_MS = 5000;
const RECONNECT_JITTER_RATIO = 0.2;

// 与后端 logbuf 缓冲容量保持一致的本地保留上限.
const MAX_LOCAL_LOG_ENTRIES = 2000;

const listeners = new Set();
const readyWaiters = new Set();

const storeState = {
	entries: [],
	count: 0,
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

const cloneEntries = (entries) => Array.isArray(entries) ? entries.map((item) => ({ ...item })) : [];

const emit = () => {
	const snapshot = getSnapshot();
	if (snapshot.ready) {
		readyWaiters.forEach((waiter) => waiter.resolve(snapshot));
		readyWaiters.clear();
	}
	listeners.forEach((listener) => {
		try {
			listener(snapshot);
		} catch (error) {
			console.error('[SSE] 运行日志订阅回调失败:', error);
		}
	});
};

const rejectReadyWaiters = (error) => {
	readyWaiters.forEach((waiter) => waiter.reject(error));
	readyWaiters.clear();
};

export const getSnapshot = () => ({
	entries: cloneEntries(storeState.entries),
	count: storeState.count,
	version: storeState.version,
	ready: storeState.ready,
});

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

// applyLogCount 处理服务端推送的日志计数; 仅维护 count, 不拉取条目.
const applyLogCount = (payload) => {
	const count = Math.max(0, Number(payload?.count) || 0);
	storeState.version = Number(payload?.version) || 0;
	if (storeState.count === count) {
		return;
	}
	storeState.count = count;
	emit();
};

// loadAll 全量拉取服务端缓冲条目, 在打开 LOG 卡片时调用.
export const loadAll = async () => {
	const result = await fetchLogs({ limit: MAX_LOCAL_LOG_ENTRIES });
	if (!result.ok) {
		throw new Error(result.error || '获取运行日志失败');
	}
	const entries = Array.isArray(result.data?.entries) ? result.data.entries : [];
	// 服务端按 seq 倒序返回; 本地以 seq 升序存储.
	storeState.entries = entries.slice().reverse().map((item) => ({
		seq: Number(item?.seq) || 0,
		time: Number(item?.time) || 0,
		level: String(item?.level || ''),
		instance: String(item?.instance || ''),
		message: String(item?.message || ''),
	})).filter((item) => item.seq > 0);
	storeState.ready = true;
	emit();
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
			log_count(event) {
				try {
					applyLogCount(parseSSEJsonData(event, '运行日志计数事件解析失败'));
				} catch (error) {
					console.error('[SSE] 解析运行日志计数事件失败:', error);
				}
			},
		});
	} catch (error) {
		if (error.name !== 'AbortError') {
			console.error('[SSE] 运行日志流连接失败:', error);
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
	storeState.count = 0;
	storeState.entries = [];
	rejectReadyWaiters(new Error('运行日志流已停止'));
	emit();
};

// resetLocal 清空本地条目视图并将计数归零 (服务端已清空).
export const resetLocal = () => {
	storeState.entries = [];
	storeState.count = 0;
	emit();
};

export const logStore = {
	start,
	stop,
	subscribe,
	getSnapshot,
	waitForReady,
	loadAll,
	resetLocal,
};
