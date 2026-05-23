import { showAlert } from '../module/dialog.js';

const STORAGE_KEY_PREFIX = 'IpacPanel';
const CSRF_COOKIE_KEY = 'csrf';
const CSRF_HEADER_KEY = 'X-CSRF-Token';

const getCookieValue = (name) => {
	try {
		const target = `${String(name || '').trim()}=`;
		if (!target || target === '=') {
			return '';
		}
		const parts = String(document.cookie || '').split(';');
		for (const part of parts) {
			const item = part.trim();
			if (!item.startsWith(target)) {
				continue;
			}
			return decodeURIComponent(item.slice(target.length));
		}
	} catch {
		// ignore
	}
	return '';
};

const clearStorageByPrefix = (storage, prefix) => {
	try {
		if (!storage) return;
		const p = String(prefix || '');
		if (!p) return;

		const keys = [];
		for (let i = 0; i < storage.length; i++) {
			const k = storage.key(i);
			if (!k) continue;
			if (k.startsWith(p)) {
				keys.push(k);
			}
		}
		for (const k of keys) {
			try {
				storage.removeItem(k);
			} catch {
				// ignore
			}
		}
	} catch {
		// ignore
	}
};

export const getCSRFToken = () => getCookieValue(CSRF_COOKIE_KEY);

export const getErrorMessage = (error) => error?.message || String(error);

export const createApiSuccess = (data = null, extra = {}) => ({
	ok: true,
	data,
	error: '',
	...extra,
});

export const createApiFailure = (error, extra = {}) => ({
	ok: false,
	data: null,
	error: getErrorMessage(error),
	unauthorized: error?.status === 401 || error?.message === 'HTTP 401',
	...extra,
});

const getApiErrorMessage = (payload) => {
	if (!payload || typeof payload !== 'object') {
		return '';
	}
	return String(
		payload.message
		|| payload.error
		|| payload.data?.message
		|| payload.data?.error
		|| ''
	).trim();
};

export const createHttpError = async (res) => {
	const raw = await res.text().catch(() => '');
	let message = '';
	if (raw) {
		try {
			message = getApiErrorMessage(JSON.parse(raw));
		} catch {
			message = raw;
		}
	}
	const error = new Error(message || `HTTP ${res.status}`);
	error.status = res.status;
	return error;
};

export const parseJsonSafe = async (res) => res.json().catch(() => null);

export const parseJsonData = async (res) => {
	if (!res.ok) {
		throw await createHttpError(res);
	}
	const payload = await parseJsonSafe(res);
	if (payload && Object.prototype.hasOwnProperty.call(payload, 'data')) {
		return payload.data;
	}
	return null;
};

export const parseJsonPayload = async (res) => {
	if (!res.ok) {
		throw await createHttpError(res);
	}
	return await parseJsonSafe(res);
};

export const postJson = async (url, payload = {}, options = {}) => {
	const requestOptions = Object.assign({}, options);
	delete requestOptions.headers;
	const headers = Object.assign({
		'Content-Type': 'application/json',
	}, options.headers || {});
	const res = await authedFetch(url, Object.assign(requestOptions, {
		method: 'POST',
		headers,
		body: JSON.stringify(payload || {}),
	}));
	return res;
};

export const postJsonData = async (url, payload = {}, options = {}) => {
	const res = await postJson(url, payload, options);
	return await parseJsonData(res);
};

export const postEventStream = async (url, payload = {}, options = {}) => {
	const requestOptions = Object.assign({}, options);
	delete requestOptions.headers;
	const headers = Object.assign({
		'Content-Type': 'application/json',
		'Accept': 'text/event-stream',
	}, options.headers || {});
	const res = await authedFetch(url, Object.assign(requestOptions, {
		method: 'POST',
		headers,
		body: JSON.stringify(payload || {}),
	}));
	if (!res.ok) {
		throw await createHttpError(res);
	}
	return res;
};

export const createSSEParser = (onEvent) => {
	let buffer = '';
	let eventName = 'message';
	let dataLines = [];
	const dispatch = () => {
		if (dataLines.length === 0) {
			eventName = 'message';
			return;
		}
		onEvent({
			type: eventName || 'message',
			data: dataLines.join('\n'),
		});
		eventName = 'message';
		dataLines = [];
	};
	const handleLine = (line) => {
		if (line === '') {
			dispatch();
			return;
		}
		if (line.startsWith(':')) {
			return;
		}
		const colonIndex = line.indexOf(':');
		const field = colonIndex >= 0 ? line.slice(0, colonIndex) : line;
		const rawValue = colonIndex >= 0 ? line.slice(colonIndex + 1) : '';
		const value = rawValue.startsWith(' ') ? rawValue.slice(1) : rawValue;
		if (field === 'event') {
			eventName = value;
			return;
		}
		if (field === 'data') {
			dataLines.push(value);
		}
	};
	return {
		feed(chunk) {
			buffer += String(chunk || '');
			const lines = buffer.split(/\r\n|\n|\r/);
			buffer = lines.pop() || '';
			for (const line of lines) {
				handleLine(line);
			}
		},
		finish() {
			if (buffer) {
				handleLine(buffer);
				buffer = '';
			}
			dispatch();
		},
	};
};

export const readSSEStream = async (res, handlers = {}) => {
	if (!res.body || typeof res.body.getReader !== 'function') {
		throw new Error('SSE 流正文不可用');
	}
	const decoder = new TextDecoder();
	const parser = createSSEParser((event) => {
		const handler = handlers[event.type] || handlers.message;
		if (typeof handler === 'function') {
			handler(event);
		}
	});
	const reader = res.body.getReader();
	try {
		while (true) {
			const { done, value } = await reader.read();
			if (done) {
				break;
			}
			parser.feed(decoder.decode(value, { stream: true }));
		}
		parser.feed(decoder.decode());
		parser.finish();
	} finally {
		reader.releaseLock();
	}
};

export const parseSSEJsonData = (event, errorPrefix = 'SSE 数据解析失败') => {
	try {
		return JSON.parse(event.data || 'null');
	} catch (error) {
		throw new Error(`${errorPrefix}: ${error.message || String(error)}`);
	}
};

export const withApiFallback = async (run, fallback, logMessage = '') => {
	try {
		return await run();
	} catch (e) {
		if (logMessage) {
			console.error(logMessage, e);
		}
		return typeof fallback === 'function' ? await fallback(e) : fallback;
	}
};

export const withApiResult = async (run, { logMessage = '' } = {}) => {
	try {
		const data = await run();
		return createApiSuccess(data);
	} catch (error) {
		if (logMessage) {
			console.error(logMessage, error);
		}
		return createApiFailure(error);
	}
};

const shouldAttachCSRF = (method) => {
	const normalized = String(method || 'GET').trim().toUpperCase();
	return normalized !== 'GET' && normalized !== 'HEAD' && normalized !== 'OPTIONS';
};

export const clearAllStoredData = () => {
	clearStorageByPrefix(localStorage, STORAGE_KEY_PREFIX);
	clearStorageByPrefix(sessionStorage, STORAGE_KEY_PREFIX);
};

const UNAUTHORIZED_EVENT = 'ipacpanel:unauthorized';
const AUTHENTICATED_EVENT = 'ipacpanel:authenticated';

export const UNAUTHORIZED_REASON_RUNTIME = 'runtime';
export const UNAUTHORIZED_REASON_LOGOUT = 'logout';
export const AUTHENTICATED_REASON_LOGIN_SUCCESS = 'login_success';

export const clearAllStoredDataAndEnterUnauthorizedState = () => {
	clearAllStoredData();
	window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, {
		detail: {
			reason: UNAUTHORIZED_REASON_LOGOUT,
			clearStoredData: false,
		},
	}));
};

export const dispatchUnauthorized = (detail = {}) => {
	window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, {
		detail: {
			reason: UNAUTHORIZED_REASON_RUNTIME,
			clearStoredData: false,
			...detail,
		},
	}));
};

export const addUnauthorizedListener = (listener) => {
	if (typeof listener !== 'function') {
		return () => {};
	}
	const handler = (event) => {
		listener(event?.detail || {});
	};
	window.addEventListener(UNAUTHORIZED_EVENT, handler);
	return () => {
		window.removeEventListener(UNAUTHORIZED_EVENT, handler);
	};
};

export const dispatchAuthenticated = (detail = {}) => {
	window.dispatchEvent(new CustomEvent(AUTHENTICATED_EVENT, {
		detail: {
			reason: AUTHENTICATED_REASON_LOGIN_SUCCESS,
			...detail,
		},
	}));
};

export const addAuthenticatedListener = (listener) => {
	if (typeof listener !== 'function') {
		return () => {};
	}
	const handler = (event) => {
		listener(event?.detail || {});
	};
	window.addEventListener(AUTHENTICATED_EVENT, handler);
	return () => {
		window.removeEventListener(AUTHENTICATED_EVENT, handler);
	};
};

export const authedFetch = async (url, options = {}) => {
	const headers = Object.assign({}, options.headers || {});
	const skipUnauthorizedReload = options.skipUnauthorizedReload === true;
	const method = String(options.method || 'GET').trim().toUpperCase();
	if (shouldAttachCSRF(method)) {
		const csrfToken = getCSRFToken();
		if (csrfToken) {
			headers[CSRF_HEADER_KEY] = csrfToken;
		}
	}
	const requestOptions = Object.assign({}, options, {
		headers,
		credentials: 'same-origin',
	});
	delete requestOptions.skipUnauthorizedReload;
	const res = await fetch(url, requestOptions);
	if (res.status === 401 && !skipUnauthorizedReload) {
		dispatchUnauthorized();
	}
	return res;
};

export const buildAuthedFileRawUrl = (name, path, options = {}) => {
	const instanceName = String(name || '').trim();
	const filePath = String(path || '').trim();
	if (!instanceName || !filePath) {
		return '';
	}
	const query = new URLSearchParams({
		instance: instanceName,
		path: filePath,
	});
	if (options.download) {
		query.set('download', '1');
	}
	return `/api/file/raw?${query.toString()}`;
};
