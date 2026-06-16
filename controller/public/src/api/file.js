import {
	authedFetch,
	getCSRFToken,
	dispatchUnauthorized,
	buildXhrUploadErrorMessage,
	getXhrJsonResponseData,
	createHttpError,
	postEventStream,
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

const postFileActionSilent = async (url, payload, options = {}) => {
	const res = await authedFetch(url, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json'
		},
		body: JSON.stringify(payload),
		signal: options.signal || undefined,
	});
	return await parseJsonData(res);
};

const triggerSilentDownload = (url) => {
	const downloadUrl = String(url || '').trim();
	if (!downloadUrl) {
		throw new Error('下载地址为空');
	}
	const iframe = document.createElement('iframe');
	iframe.src = downloadUrl;
	iframe.style.display = 'none';
	iframe.setAttribute('aria-hidden', 'true');
	document.body.appendChild(iframe);
	setTimeout(() => {
		iframe.remove();
	}, 5 * 60 * 1000);
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

const readEncodedFileHeader = (headers, headerName) => {
	const raw = headers.get(headerName);
	if (raw === null || raw === '') {
		throw new Error(`读取文件响应头缺少 ${headerName}`);
	}
	try {
		return decodeURIComponent(raw);
	} catch (error) {
		throw new Error(`读取文件响应头 ${headerName} 失败: ${error.message || String(error)}`);
	}
};

const readFileSizeHeader = (headers, buffer) => {
	const raw = headers.get('X-File-Size');
	if (raw === null || raw === '') {
		throw new Error('读取文件响应头缺少 X-File-Size');
	}
	const size = Number(raw);
	if (!Number.isSafeInteger(size) || size < 0) {
		throw new Error(`读取文件响应头 X-File-Size 失败: ${raw}`);
	}
	if (size !== buffer.byteLength) {
		throw new Error(`读取文件大小不一致: 响应头 ${size}, 实际 ${buffer.byteLength}`);
	}
	return size;
};

const isAbortError = (error) => error && error.name === 'AbortError';

const requireCompletedUploadResponse = (result) => {
	if (!result || typeof result !== 'object' || result.completed !== true) {
		throw new Error('上传协议异常: 缺少完成响应');
	}
	return result;
};

const uploadTextChunkWithRetry = async (instanceName, uploadId, index, chunk, options = {}) => {
	let lastError = null;
	for (let attempt = 1; attempt <= TEXT_UPLOAD_RETRY_COUNT; attempt += 1) {
		try {
			return await uploadFileChunk(instanceName, uploadId, index, chunk, null, { signal: options.signal || null });
		} catch (error) {
			lastError = error;
			if (isAbortError(error) || attempt >= TEXT_UPLOAD_RETRY_COUNT) {
				break;
			}
			await wait(TEXT_UPLOAD_RETRY_DELAY_MS);
		}
	}
	throw lastError || new Error(`分块 ${index} 上传失败`);
};

export const completeFileUploadWithRetry = async (name, uploadId, options = {}) => {
	const retryCount = Math.max(1, Number(options.retryCount) || TEXT_UPLOAD_RETRY_COUNT);
	const retryDelay = Math.max(0, Number(options.retryDelay) || TEXT_UPLOAD_RETRY_DELAY_MS);
	const signal = options.signal || null;
	let lastError = null;
	for (let attempt = 1; attempt <= retryCount; attempt += 1) {
		if (signal && signal.aborted) {
			const err = new Error('中止');
			err.name = 'AbortError';
			throw err;
		}
		try {
			return requireCompletedUploadResponse(await completeFileUpload(name, uploadId, { signal }));
		} catch (error) {
			lastError = error;
			if (isAbortError(error) || attempt >= retryCount) {
				break;
			}
			await wait(retryDelay * attempt);
		}
	}
	throw lastError || new Error('上传完成确认失败');
};

const uploadTextBlobAsFile = async (instanceName, dirPath, fileName, blob, overwrite, options = {}) => {
	return await withApiResult(async () => {
		const signal = options.signal || null;
		if (signal && signal.aborted) {
			const err = new Error('中止');
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
			const chunkResults = new Array(chunkCount).fill(null);
			const worker = async () => {
				while (nextIndex < chunkCount) {
					if (signal && signal.aborted) {
						const err = new Error('中止');
						err.name = 'AbortError';
						throw err;
					}
					const index = nextIndex;
					nextIndex += 1;
					const start = index * chunkSize;
					const end = Math.min(size, start + chunkSize);
					chunkResults[index] = await uploadTextChunkWithRetry(instanceName, uploadId, index, blob.slice(start, end), { signal });
				}
			};
			const workers = Array.from({ length: Math.min(TEXT_UPLOAD_CONCURRENCY, chunkCount) }, () => worker());
			await Promise.all(workers);
			if (signal && signal.aborted) {
				const err = new Error('中止');
				err.name = 'AbortError';
				throw err;
			}
			let result = chunkResults.find((item) => item && typeof item === 'object' && item.completed === true);
			if (!result) {
				result = await completeFileUploadWithRetry(instanceName, uploadId, { signal });
			}
			result = requireCompletedUploadResponse(result);
			shouldAbort = false;
			return result;
		} finally {
			if (shouldAbort) {
				await abortFileUpload(instanceName, uploadId).catch(() => null);
			}
		}
	}, { logMessage: `[API] 上传文本文件 ${fileName} 失败:` });
};

export const fetchFiles = async (name, path = '', fallback = false, page = 1, search = '', options = {}) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/file/list', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				instance: name,
				path,
				fallback: fallback === true,
				page: Math.max(1, Number(page) || 1),
				query: String(search || '').trim(),
				jump: !!(options && options.jump),
			}),
		});
		return await parseJsonData(res);
	}, {
		logMessage: `[API] 获取实例 ${name} 文件列表失败:`,
	});
};

export const downloadFileArchive = async (name, include, exclude = [], fallbackName = 'archive.zip') => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/file/archive', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				instance: name,
				include: Array.isArray(include) ? include : [],
				exclude: Array.isArray(exclude) ? exclude : [],
			}),
		});
		const data = await parseJsonData(res);
		const downloadUrl = String(data && data.download_url ? data.download_url : '').trim();
		const filename = String(data && data.filename ? data.filename : fallbackName).trim() || 'archive.zip';
		triggerSilentDownload(downloadUrl);
		return { download_url: downloadUrl, filename };
	}, {
		logMessage: '[API] 打包下载失败:',
	});
};

export const createEmptyFile = async (name, path, fileName, content = '', overwrite = false) => {
	return await postFileAction('/api/file/create/file', {
		instance: name,
		path,
		name: fileName,
		content,
		overwrite: !!overwrite,
	}, '创建文件');
};

export const createDirectory = async (name, path, dirName) => {
	return await postFileAction('/api/file/create/dir', {
		instance: name,
		path,
		name: dirName,
	}, '创建目录');
};

export const renameFile = async (name, path, newName) => {
	return await postFileAction('/api/file/rename', {
		instance: name,
		path,
		new_name: newName,
	}, '重命名');
};

export const deleteFile = async (name, path) => {
	return await postFileAction('/api/file/delete', {
		instance: name,
		path,
	}, '删除');
};

export const readFileContent = async (name, path, options = {}) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/file/read', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				instance: name,
				path,
				allow_large: !!(options && options.allowLarge),
			}),
		});
		if (!res.ok) {
			throw await createHttpError(res);
		}
		const buffer = await res.arrayBuffer().catch((error) => {
			throw new Error(`读取文件内容失败: ${error.message || String(error)}`);
		});
		const responsePath = readEncodedFileHeader(res.headers, 'X-File-Path');
		const responseName = readEncodedFileHeader(res.headers, 'X-File-Name');
		if (!responseName) {
			throw new Error('读取文件响应头缺少文件名');
		}
		return {
			path: responsePath,
			name: responseName,
			size: readFileSizeHeader(res.headers, buffer),
			content: new TextDecoder('utf-8', { fatal: false }).decode(buffer),
		};
	}, {
		logMessage: `[API] 读取文件 ${path} 失败:`,
	});
};

export const saveFileContentDetailed = async (name, path, content) => {
	return await withApiResult(async () => {
		const res = await authedFetch('/api/file/save', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ instance: name, path, content })
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
	return await postFileActionSilent('/api/file/upload/init', Object.assign({}, payload || {}, { instance: name }));
};

export const abortFileUpload = async (name, uploadId) => {
	return await postFileActionSilent('/api/file/upload/abort', {
		instance: name,
		upload_id: uploadId,
	});
};

export const completeFileUpload = async (name, uploadId, options = {}) => {
	return await postFileActionSilent('/api/file/upload/complete', {
		instance: name,
		upload_id: uploadId,
	}, options);
};

const uploadBinaryWithXhr = (url, body, headers, onProgress, options = {}) => new Promise((resolve, reject) => {
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
			const err = new Error('中止');
			err.name = 'AbortError';
			reject(err);
			return;
		}
		signal.addEventListener('abort', onAbort, { once: true });
	}

	xhr.open('POST', url);
	const csrfToken = getCSRFToken();
	if (csrfToken) {
		xhr.setRequestHeader('X-CSRF-Token', csrfToken);
	}
	Object.entries(headers || {}).forEach(([key, value]) => {
		xhr.setRequestHeader(key, String(value));
	});
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
			const err = new Error('未授权');
			err.name = 'UnauthorizedError';
			reject(err);
			return;
		}
		if (xhr.status >= 200 && xhr.status < 300) {
			const responseData = getXhrJsonResponseData(xhr);
			resolve(responseData || xhr.response || { ok: true });
			return;
		}

		const message = await buildXhrUploadErrorMessage(xhr);
		reject(new Error(message));
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
	xhr.send(body);
});

export const uploadFileChunk = async (name, uploadId, index, chunk, onProgress, options = {}) => {
	return await uploadBinaryWithXhr('/api/file/upload/chunk', chunk, {
		'X-Ipac-Upload-Id': uploadId || '',
		'X-Ipac-Chunk-Index': index,
		'X-Ipac-Instance': name || '',
	}, onProgress, options);
};

export const streamFileBatchAction = async (name, payload) => {
	return await postEventStream('/api/file/batch', Object.assign({}, payload || {}, { instance: name }));
};

export const streamFileExtractAction = async (name, payload, options = {}) => {
	return await postEventStream('/api/file/extract', Object.assign({}, payload || {}, { instance: name }), {
		signal: options && options.signal ? options.signal : undefined,
	});
};
