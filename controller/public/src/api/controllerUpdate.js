import {
	authedFetch,
	getCSRFToken,
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

export const applyControllerUpdate = async () => {
	return await postJson('/api/controller/update/apply');
};

export const uploadControllerUpdateChunk = (uploadId, index, chunk, onProgress) => new Promise((resolve, reject) => {
	const xhr = new XMLHttpRequest();
	const query = new URLSearchParams({
		upload_id: uploadId,
		index: String(index),
	});
	xhr.open('POST', `/api/controller/update/upload/chunk?${query.toString()}`);
	const csrfToken = getCSRFToken();
	if (csrfToken) {
		xhr.setRequestHeader('X-CSRF-Token', csrfToken);
	}
	xhr.responseType = 'json';
	xhr.upload.onprogress = (event) => {
		if (event.lengthComputable && typeof onProgress === 'function') {
			onProgress(event.loaded, event.total);
		}
	};
	xhr.onload = () => {
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
		reject(new Error('Network error'));
	};
	xhr.onabort = () => {
		const err = new Error('aborted');
		err.name = 'AbortError';
		reject(err);
	};
	xhr.send(chunk);
});
