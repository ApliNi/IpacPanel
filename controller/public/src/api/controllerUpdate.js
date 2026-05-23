import {
	authedFetch,
	getCSRFToken,
	dispatchUnauthorized,
	parseJsonData,
	withApiResult,
} from './core.js';

const postJson = async (url, payload = {}) => {
	const res = await authedFetch(url, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
		},
		body: JSON.stringify(payload),
	});
	return await parseJsonData(res);
};

export const fetchControllerUpdateStatus = async () => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/controller/update/status');
		return await parseJsonData(res);
	}, { logMessage: '[API] 获取面板更新状态失败:' });
};

export const initControllerUpdateUpload = async (payload) => {
	return await postJson('/api/controller/update/upload/init', payload);
};

export const completeControllerUpdateUpload = async (uploadId) => {
	return await postJson('/api/controller/update/upload/complete', { upload_id: uploadId });
};

export const abortControllerUpdateUpload = async (uploadId) => {
	return await postJson('/api/controller/update/upload/abort', { upload_id: uploadId });
};

export const applyControllerUpdate = async () => {
	return await postJson('/api/controller/update/apply');
};

export const uploadControllerUpdateChunk = (uploadId, index, chunk, onProgress, options = {}) => new Promise((resolve, reject) => {
	const xhr = new XMLHttpRequest();
	const signal = options && options.signal ? options.signal : null;
	let cleaned = false;
	const cleanup = () => {
		if (cleaned) {
			return;
		}
		cleaned = true;
		if (signal) {
			signal.removeEventListener('abort', onAbort);
		}
	};
	const onAbort = () => {
		try {
			xhr.abort();
		} catch (e) {
			// ignore
		}
	};
	if (signal) {
		if (signal.aborted) {
			const err = new Error('aborted');
			err.name = 'AbortError';
			reject(err);
			return;
		}
		signal.addEventListener('abort', onAbort, { once: true });
	}
	xhr.open('POST', '/api/controller/update/upload/chunk');
	const csrfToken = getCSRFToken();
	if (csrfToken) {
		xhr.setRequestHeader('X-CSRF-Token', csrfToken);
	}
	xhr.setRequestHeader('X-Ipac-Upload-Id', String(uploadId || ''));
	xhr.setRequestHeader('X-Ipac-Chunk-Index', String(index));
	xhr.responseType = 'json';
	xhr.upload.onprogress = (event) => {
		if (event.lengthComputable && typeof onProgress === 'function') {
			onProgress(event.loaded, event.total);
		}
	};
	xhr.onload = () => {
		cleanup();
		if (xhr.status === 401) {
			dispatchUnauthorized();
			const err = new Error('未授权');
			err.name = 'UnauthorizedError';
			reject(err);
			return;
		}
		if (xhr.status >= 200 && xhr.status < 300) {
			if (xhr.response && Object.prototype.hasOwnProperty.call(xhr.response, 'data')) {
				resolve(xhr.response.data);
				return;
			}
			resolve(xhr.response);
			return;
		}
		const responseMessage = xhr.response && xhr.response.message ? xhr.response.message : '';
		reject(new Error(responseMessage || xhr.responseText || `HTTP ${xhr.status}`));
	};
	xhr.onerror = () => {
		cleanup();
		reject(new Error('Network Error'));
	};
	xhr.onabort = () => {
		cleanup();
		const err = new Error('aborted');
		err.name = 'AbortError';
		reject(err);
	};
	xhr.send(chunk);
});
