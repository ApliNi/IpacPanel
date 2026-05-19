import {
	authedFetch,
	getCSRFToken,
	dispatchUnauthorized,
	parseJsonData,
	withApiResult,
} from './core.js';

const postFileAction = async (url, payload, actionText) => {
	return await withApiResult(async () => {
		const res = await authedFetch(url, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(payload)
		});
		return await parseJsonData(res);
	}, {
		logMessage: `[API] ${actionText}失败:`,
	});
};

const postFileActionSilent = async (url, payload) => {
	const res = await authedFetch(url, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json'
		},
		body: JSON.stringify(payload)
	});
	return await parseJsonData(res);
};

const TEXT_UPLOAD_THRESHOLD_BYTES = 9 * 1024 * 1024;
const TEXT_UPLOAD_CHUNK_SIZE_BYTES = 9 * 1024 * 1024;
const TEXT_UPLOAD_CONCURRENCY = 4;
const TEXT_UPLOAD_RETRY_COUNT = 7;
const TEXT_UPLOAD_RETRY_DELAY_MS = 500;

const wait = (ms) => new Promise(resolve => setTimeout(resolve, ms));

const createTextBlob = (content) => new Blob([String(content == null ? '' : content)], { type: 'text/plain;charset=utf-8' });

const splitFilePath = (path) => {
	const normalized = String(path || '').replaceAll('\\', '/').replace(/^\/+/, '').replace(/\/+$/g, '');
	const slash = normalized.lastIndexOf('/');
	if (slash < 0) {
		return { dir: '', name: normalized };
	}
	return {
		dir: normalized.slice(0, slash),
		name: normalized.slice(slash + 1),
	};
};

const isAbortError = (error) => error && error.name === 'AbortError';

const uploadTextChunkWithRetry = async (instanceName, uploadId, index, chunk, options = {}) => {
	let lastError = null;
	for (let attempt = 1; attempt <= TEXT_UPLOAD_RETRY_COUNT; attempt += 1) {
		try {
			await uploadFileChunk(instanceName, uploadId, index, chunk, null, { signal: options.signal || null });
			return;
		} catch (error) {
			lastError = error;
			if (isAbortError(error) || attempt >= TEXT_UPLOAD_RETRY_COUNT) {
				break;
			}
			await wait(TEXT_UPLOAD_RETRY_DELAY_MS);
		}
	}
	throw lastError || new Error(`Chunk ${index} upload failed`);
};

const uploadTextBlobAsFile = async (instanceName, dirPath, fileName, blob, overwrite, options = {}) => {
	return await withApiResult(async () => {
		const signal = options.signal || null;
		if (signal && signal.aborted) {
			const err = new Error('aborted');
			err.name = 'AbortError';
			throw err;
		}
		const size = blob.size;
		const chunkSize = size > TEXT_UPLOAD_CHUNK_SIZE_BYTES ? TEXT_UPLOAD_CHUNK_SIZE_BYTES : Math.max(size, 1);
		const chunkCount = Math.max(1, Math.ceil(size / chunkSize));
		const initResult = await initFileUpload(instanceName, {
			path: dirPath,
			name: fileName,
			size,
			chunk_size: chunkSize,
			chunk_count: chunkCount,
			overwrite: !!overwrite,
		});
		const uploadId = initResult && typeof initResult === 'object' ? initResult.upload_id : '';
		if (!uploadId) {
			throw new Error('UPLOAD INIT FAILED');
		}

		let shouldAbort = true;
		try {
			let nextIndex = 0;
			const worker = async () => {
				while (nextIndex < chunkCount) {
					if (signal && signal.aborted) {
						const err = new Error('aborted');
						err.name = 'AbortError';
						throw err;
					}
					const index = nextIndex;
					nextIndex += 1;
					const start = index * chunkSize;
					const end = Math.min(size, start + chunkSize);
					await uploadTextChunkWithRetry(instanceName, uploadId, index, blob.slice(start, end), { signal });
				}
			};
			const workers = Array.from({ length: Math.min(TEXT_UPLOAD_CONCURRENCY, chunkCount) }, () => worker());
			await Promise.all(workers);
			if (signal && signal.aborted) {
				const err = new Error('aborted');
				err.name = 'AbortError';
				throw err;
			}
			const result = await completeFileUpload(instanceName, uploadId);
			shouldAbort = false;
			return result;
		} finally {
			if (shouldAbort) {
				await abortFileUpload(instanceName, uploadId).catch(() => null);
			}
		}
	}, { logMessage: `[API] 上传文本文件 ${fileName} 失败:` });
};

export const fetchFiles = async (name, path = '', fallback = false, cursor = '', search = '', options = {}) => {
	return await withApiResult(async () => {
		const params = new URLSearchParams({ name });
		if (path) {
			params.set('path', path);
		}
		if (fallback) {
			params.set('fallback', '1');
		}
		if (String(cursor || '').trim()) {
			params.set('cursor', String(cursor || '').trim());
		}
		if (String(search || '').trim()) {
			params.set('query', String(search || '').trim());
		}
		if (options && options.jump) {
			params.set('jump', '1');
		}
		const res = await authedFetch(`/api/file/list?${params.toString()}`);
		return await parseJsonData(res);
	}, {
		logMessage: `[API] 获取实例 ${name} 文件列表失败:`,
	});
};

export const createEmptyFile = async (name, path, fileName, content = '', overwrite = false) => {
	return await postFileAction(`/api/file/create/file?name=${encodeURIComponent(name)}`, {
		path,
		name: fileName,
		content,
		overwrite: !!overwrite,
	}, '创建文件');
};

export const createDirectory = async (name, path, dirName) => {
	return await postFileAction(`/api/file/create/dir?name=${encodeURIComponent(name)}`, {
		path,
		name: dirName,
	}, '创建目录');
};

export const renameFile = async (name, path, newName) => {
	return await postFileAction(`/api/file/rename?name=${encodeURIComponent(name)}`, {
		path,
		new_name: newName,
	}, '重命名');
};

export const deleteFile = async (name, path) => {
	return await postFileAction(`/api/file/delete?name=${encodeURIComponent(name)}`, {
		path,
	}, '删除');
};

export const readFileContent = async (name, path, options = {}) => {
	return await withApiResult(async () => {
		const query = new URLSearchParams({ name, path });
		if (options && options.allowLarge) {
			query.set('allow_large', '1');
		}
		const res = await authedFetch(`/api/file/read?${query.toString()}`);
		return await parseJsonData(res);
	}, {
		logMessage: `[API] 读取文件 ${path} 失败:`,
	});
};

export const saveFileContentDetailed = async (name, path, content) => {
	return await withApiResult(async () => {
		const res = await authedFetch(`/api/file/save?name=${encodeURIComponent(name)}`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ path, content })
		});
		return await parseJsonData(res);
	}, { logMessage: `[API] 保存文件 ${path} 失败:` });
};

export const createTextFileAdaptive = async (name, path, fileName, content = '', overwrite = false, options = {}) => {
	const blob = createTextBlob(content);
	if (blob.size <= TEXT_UPLOAD_THRESHOLD_BYTES) {
		return await createEmptyFile(name, path, fileName, content, overwrite);
	}
	return await uploadTextBlobAsFile(name, path, fileName, blob, overwrite, options);
};

export const saveFileContentAdaptive = async (name, path, content, options = {}) => {
	const blob = createTextBlob(content);
	if (blob.size <= TEXT_UPLOAD_THRESHOLD_BYTES) {
		return await saveFileContentDetailed(name, path, content);
	}
	const target = splitFilePath(path);
	if (!target.name) {
		return await withApiResult(async () => {
			throw new Error('文件路径无效');
		}, { logMessage: `[API] 保存文件 ${path} 失败:` });
	}
	return await uploadTextBlobAsFile(name, target.dir, target.name, blob, true, options);
};

export const initFileUpload = async (name, payload) => {
	return await postFileActionSilent(`/api/file/upload/init?name=${encodeURIComponent(name)}`, payload);
};

export const completeFileUpload = async (name, uploadId) => {
	return await postFileActionSilent(`/api/file/upload/complete?name=${encodeURIComponent(name)}`, {
		upload_id: uploadId,
	});
};

export const abortFileUpload = async (name, uploadId) => {
	return await postFileActionSilent(`/api/file/upload/abort?name=${encodeURIComponent(name)}`, {
		upload_id: uploadId,
	});
};

export const uploadFileChunk = (name, uploadId, index, chunk, onProgress, options = {}) => new Promise((resolve, reject) => {
	const xhr = new XMLHttpRequest();
	const query = new URLSearchParams({
		name,
		upload_id: uploadId,
		index: String(index),
	});

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

	xhr.open('POST', `/api/file/upload/chunk?${query.toString()}`);
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

	xhr.onload = async () => {
		cleanup();
		if (xhr.status === 401) {
			dispatchUnauthorized();
			const err = new Error('unauthorized');
			err.name = 'UnauthorizedError';
			reject(err);
			return;
		}
		if (xhr.status >= 200 && xhr.status < 300) {
			const responseData = xhr.response && typeof xhr.response === 'object' ? xhr.response.data : null;
			resolve(responseData || xhr.response || { ok: true });
			return;
		}

		let message = `HTTP ${xhr.status}`;
		if (typeof xhr.response === 'string' && xhr.response) {
			message = xhr.response;
		} else if (xhr.response instanceof Blob) {
			message = await xhr.response.text();
		} else if (xhr.response && xhr.response.message) {
			message = xhr.response.message;
		} else if (xhr.responseText) {
			message = xhr.responseText;
		}
		reject(new Error(message));
	};

	xhr.onerror = () => {
		cleanup();
		reject(new Error('Network error'));
	};

	xhr.onabort = () => {
		cleanup();
		const err = new Error('aborted');
		err.name = 'AbortError';
		reject(err);
	};
	xhr.send(chunk);
});

export const streamFileBatchAction = async (name, payload) => {
	const res = await authedFetch(`/api/file/batch?name=${encodeURIComponent(name)}`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'Accept': 'text/event-stream',
		},
		body: JSON.stringify(payload || {}),
	});
	if (!res.ok) {
		const err = await res.text().catch(() => '');
		throw new Error(err || `HTTP ${res.status}`);
	}
	return res;
};

export const streamFileExtractAction = async (name, payload, options = {}) => {
	const res = await authedFetch(`/api/file/extract?name=${encodeURIComponent(name)}`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'Accept': 'text/event-stream',
		},
		body: JSON.stringify(payload || {}),
		signal: options && options.signal ? options.signal : undefined,
	});
	if (!res.ok) {
		const err = await res.text().catch(() => '');
		throw new Error(err || `HTTP ${res.status}`);
	}
	return res;
};
