import { fetchLogs } from '../api/log.js';
import { sseClient } from './sseClient.js';

// 与后端 logbuf 缓冲容量保持一致的本地保留上限.
const MAX_LOCAL_LOG_ENTRIES = 2000;
const BATCH_EMIT_INTERVAL_MS = 50;

class RingBuffer {
	constructor(capacity) {
		this.capacity = capacity;
		this.buffer = new Array(capacity);
		this.head = 0;
		this.size = 0;
	}

	push(item) {
		if (this.size < this.capacity) {
			this.buffer[(this.head + this.size) % this.capacity] = item;
			this.size += 1;
		} else {
			this.buffer[this.head] = item;
			this.head = (this.head + 1) % this.capacity;
		}
	}

	last() {
		if (this.size === 0) return null;
		return this.buffer[(this.head + this.size - 1) % this.capacity];
	}

	clear() {
		this.buffer = new Array(this.capacity);
		this.head = 0;
		this.size = 0;
	}

	toArray() {
		const result = new Array(this.size);
		for (let i = 0; i < this.size; i++) {
			result[i] = this.buffer[(this.head + i) % this.capacity];
		}
		return result;
	}

	setFromArray(items) {
		this.clear();
		const start = Math.max(0, items.length - this.capacity);
		for (let i = start; i < items.length; i++) {
			this.push(items[i]);
		}
	}
}

const ringBuffer = new RingBuffer(MAX_LOCAL_LOG_ENTRIES);
const listeners = new Set();
const readyWaiters = new Set();

const storeState = {
	count: 0,
	version: 0,
	started: false,
	ready: false,
};

let cachedEntries = Object.freeze([]);
let cachedSnapshot = Object.freeze({
	entries: cachedEntries,
	count: 0,
	version: 0,
	ready: false,
});

let entriesDirty = false;
let snapshotDirty = false;
let emitTimer = null;

const updateSnapshot = () => {
	if (entriesDirty) {
		cachedEntries = Object.freeze(ringBuffer.toArray());
		entriesDirty = false;
	}
	cachedSnapshot = Object.freeze({
		entries: cachedEntries,
		count: storeState.count,
		version: storeState.version,
		ready: storeState.ready,
	});
	snapshotDirty = false;
};

const emit = () => {
	const snapshot = getSnapshot();
	if (snapshot.ready && readyWaiters.size > 0) {
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

const scheduleEmit = () => {
	if (emitTimer !== null) {
		return;
	}
	emitTimer = setTimeout(() => {
		emitTimer = null;
		flushEmit();
	}, BATCH_EMIT_INTERVAL_MS);
};

const flushEmit = () => {
	if (emitTimer !== null) {
		clearTimeout(emitTimer);
		emitTimer = null;
	}
	if (snapshotDirty) {
		updateSnapshot();
	}
	emit();
};

const rejectReadyWaiters = (error) => {
	readyWaiters.forEach((waiter) => waiter.reject(error));
	readyWaiters.clear();
};

export const getSnapshot = () => {
	if (snapshotDirty) {
		updateSnapshot();
	}
	return cachedSnapshot;
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

// applyLogCount 处理服务端推送的日志计数; 仅维护 count, 不拉取条目.
const applyLogCount = (payload) => {
	const count = Math.max(0, Number(payload?.count) || 0);
	storeState.version = Number(payload?.version) || 0;
	if (storeState.count === count) {
		return;
	}
	storeState.count = count;
	snapshotDirty = true;
	scheduleEmit();
};

// applyLogEntry 处理服务端推送的单条日志.
const applyLogEntry = (payload) => {
	const raw = payload?.entry || payload;
	if (!raw) return;
	const seq = Number(raw?.seq) || 0;
	if (seq <= 0) return;
	const entry = Object.freeze({
		seq,
		time: Number(raw?.time) || 0,
		level: String(raw?.level || ''),
		instance: String(raw?.instance || ''),
		message: String(raw?.message || ''),
	});
	let entriesChanged = false;
	if (ringBuffer.size > 0) {
		const last = ringBuffer.last();
		if (entry.seq > last.seq) {
			ringBuffer.push(entry);
			entriesChanged = true;
		} else {
			const items = ringBuffer.toArray();
			if (!items.some((item) => item.seq === entry.seq)) {
				items.push(entry);
				items.sort((a, b) => a.seq - b.seq);
				ringBuffer.setFromArray(items);
				entriesChanged = true;
			}
		}
	} else if (storeState.ready) {
		ringBuffer.push(entry);
		entriesChanged = true;
	}
	if (storeState.count < seq) {
		storeState.count = seq;
	}
	if (Number.isFinite(payload?.count)) {
		storeState.count = Math.max(storeState.count, Number(payload.count));
	}
	storeState.version = Number(payload?.version) || storeState.count;
	if (entriesChanged) {
		entriesDirty = true;
	}
	snapshotDirty = true;
	scheduleEmit();
};

// loadAll 全量拉取服务端缓冲条目, 在打开 LOG 卡片时调用.
export const loadAll = async () => {
	const result = await fetchLogs({ limit: MAX_LOCAL_LOG_ENTRIES });
	if (!result.ok) {
		throw new Error(result.error || '获取运行日志失败');
	}
	const entries = Array.isArray(result.data?.entries) ? result.data.entries : [];
	// 服务端按 seq 倒序返回; 本地以 seq 升序存储.
	const parsed = entries.slice().reverse().map((item) => Object.freeze({
		seq: Number(item?.seq) || 0,
		time: Number(item?.time) || 0,
		level: String(item?.level || ''),
		instance: String(item?.instance || ''),
		message: String(item?.message || ''),
	})).filter((item) => item.seq > 0);
	ringBuffer.setFromArray(parsed);
	storeState.ready = true;
	entriesDirty = true;
	snapshotDirty = true;
	flushEmit();
};

sseClient.on('log_count', (payload) => {
	if (!storeState.started) return;
	try {
		applyLogCount(payload);
	} catch (error) {
		console.error('[SSE] 解析运行日志计数事件失败:', error);
	}
});

sseClient.on('log_entry', (payload) => {
	if (!storeState.started) return;
	try {
		applyLogEntry(payload);
	} catch (error) {
		console.error('[SSE] 解析运行日志条目事件失败:', error);
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
};

export const stop = () => {
	if (!storeState.started) return;
	storeState.started = false;
	sseClient.stop();
	if (emitTimer !== null) {
		clearTimeout(emitTimer);
		emitTimer = null;
	}
	storeState.ready = false;
	storeState.version = 0;
	storeState.count = 0;
	ringBuffer.clear();
	entriesDirty = true;
	snapshotDirty = true;
	rejectReadyWaiters(new Error('运行日志流已停止'));
	updateSnapshot();
	emit();
};

// resetLocal 清空本地条目视图并将计数归零 (服务端已清空).
export const resetLocal = () => {
	if (emitTimer !== null) {
		clearTimeout(emitTimer);
		emitTimer = null;
	}
	ringBuffer.clear();
	storeState.count = 0;
	entriesDirty = true;
	snapshotDirty = true;
	updateSnapshot();
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

export default logStore;
