import { mainModalOverlay, state } from "../ui.js";
import { DEFAULT_UI_REFRESH_INTERVAL_MS, clearTimer, closeAnimatedModal, getUploadErrorText, openAnimatedModal, withActionsDisabled } from '../utils/utils.js';
import { abortFileUpload, completeFileUpload, createDirectory, createTextFileAdaptive, initFileUpload, uploadFileChunk } from '../api/file.js';
import { showAlert, showConfirm } from './dialog.js';
import { applyFileUploadItemView, buildFileUploadItemNode, ceilTo, renderFileUploadSummaryText } from './fileUploadView.js';
import { InputValidation } from '../utils/inputValidation.js';

console.log('[模块] FileCreateModal 加载中...');

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="fileCreateModal" class="modal-overlay">
        <div class="modal-card file-create-modal-card">
			<div class="modal-header">
				<span class="modal-title">CREATE ENTRY</span>
				<button id="fileCreateClose" class="modal-close" type="button">×</button>
			</div>
			<form id="fileCreateForm" class="modal-form" novalidate>
		        <div class="filter-group file-action-tabs file-create-tabs">
					<button id="fileCreateTypeFile" class="filter-btn active" name="file-create-type" type="button" data-type="file">NEW FILE</button>
					<button id="fileCreateTypeDir" class="filter-btn" name="file-create-type" type="button" data-type="dir">NEW DIR</button>
					<button id="fileCreateTypeUpload" class="filter-btn" name="file-create-type" type="button" data-type="upload">UPLOAD</button>
				</div>
				<div class="field-group">
					<span>TARGET DIR</span>
					<div id="fileCreateTargetDir" class="file-action-static"></div>
				</div>
				<div id="fileCreatePageFile" class="file-create-page active">
		            <div class="field-group">
						<span>FILE NAME</span>
						<input id="fileCreateName" name="name" type="text" autocomplete="off" maxlength="${InputValidation.limits.fileName}" required>
						<div id="fileCreateRecentFiles" class="file-create-recent-list hidden"></div>
					</div>
					<div class="field-group">
						<span>CONTENT</span>
						<textarea id="fileCreateContent" name="content" rows="6" placeholder=" Optional initial content"></textarea>
					</div>
					<label class="checkbox-group file-upload-overwrite">
						<input id="fileCreateOverwrite" type="checkbox">
						<span>覆盖已存在文件</span>
					</label>
                </div>
				<div id="fileCreatePageDir" class="file-create-page">
		            <div class="field-group">
						<span>DIR NAME</span>
						<input id="fileCreateDirName" name="dir-name" type="text" autocomplete="off" maxlength="${InputValidation.limits.fileName}" required>
						<div id="fileCreateRecentDirs" class="file-create-recent-list hidden"></div>
					</div>
                </div>
				<div id="fileCreatePageUpload" class="file-create-page">
					<input id="fileUploadInput" type="file" multiple hidden>
					<input id="fileUploadDirectoryInput" type="file" webkitdirectory multiple hidden>
					<button id="fileUploadDropzone" class="file-upload-dropzone" type="button">
                        <span class="file-upload-title">DROP FILE/DIR HERE</span>
                        <span class="file-upload-subtitle">OR CLICK TO SELECT</span>
                    </button>
                    <label class="checkbox-group file-upload-overwrite">
						<input id="fileUploadOverwrite" type="checkbox">
                        <span>覆盖已存在文件</span>
                    </label>
                    <div class="file-upload-summary-row">
						<span id="fileUploadSummary" class="file-upload-summary"></span>
					</div>
					<div id="fileUploadList" class="file-upload-list"></div>
				</div>
		        <div class="modal-actions">
					<button class="btn" type="button" id="fileCreateCancel">CANCEL</button>
					<button class="btn btn-start" type="submit" id="fileCreateSubmit">CREATE</button>
				</div>
            </form>
        </div>
    </div>
`);

const dom = {
	fileCreateModal: document.getElementById('fileCreateModal'),
	fileCreateForm: document.getElementById('fileCreateForm'),
	fileCreateClose: document.getElementById('fileCreateClose'),
	fileCreateCancel: document.getElementById('fileCreateCancel'),
	fileCreateName: document.getElementById('fileCreateName'),
	fileCreateContent: document.getElementById('fileCreateContent'),
	fileCreateOverwrite: document.getElementById('fileCreateOverwrite'),
	fileCreateRecentFiles: document.getElementById('fileCreateRecentFiles'),
	fileCreateDirName: document.getElementById('fileCreateDirName'),
	fileCreateRecentDirs: document.getElementById('fileCreateRecentDirs'),
	fileCreateTypeFile: document.getElementById('fileCreateTypeFile'),
	fileCreateTypeDir: document.getElementById('fileCreateTypeDir'),
	fileCreateTypeUpload: document.getElementById('fileCreateTypeUpload'),
	fileCreateTargetDir: document.getElementById('fileCreateTargetDir'),
	fileCreatePageFile: document.getElementById('fileCreatePageFile'),
	fileCreatePageDir: document.getElementById('fileCreatePageDir'),
	fileCreatePageUpload: document.getElementById('fileCreatePageUpload'),
	fileCreateSubmit: document.getElementById('fileCreateSubmit'),
	fileUploadInput: document.getElementById('fileUploadInput'),
	fileUploadDirectoryInput: document.getElementById('fileUploadDirectoryInput'),
	fileUploadDropzone: document.getElementById('fileUploadDropzone'),
	fileUploadOverwrite: document.getElementById('fileUploadOverwrite'),
	fileUploadSummary: document.getElementById('fileUploadSummary'),
	fileUploadList: document.getElementById('fileUploadList'),
	fileCreateActions: document.querySelector('#fileCreateForm .modal-actions'),
};

const FILE_CREATE_RECENT_LIMIT = 10;
const RECENT_FILE_NAMES_KEY = 'pCmd.recentCreatedFileNames';
const RECENT_DIR_NAMES_KEY = 'pCmd.recentCreatedDirNames';

const truncateInputValue = (input, maxLength) => {
	const value = InputValidation.truncateText(input?.value || '', maxLength);
	if (input) {
		input.value = value;
	}
	return value;
};

const getUploadItemId = (itemOrId) => typeof itemOrId === 'string' ? itemOrId : String(itemOrId?.id || '');

const rememberUploadItem = (item) => {
	const id = getUploadItemId(item);
	if (!id || !item) {
		return;
	}
	modalState.fileUploadItemById.set(id, item);
};

const forgetUploadItem = (itemOrId) => {
	const id = getUploadItemId(itemOrId);
	if (!id) {
		return;
	}
	modalState.fileUploadItemById.delete(id);
	modalState.fileUploadRowById.delete(id);
	modalState.fileUploadRemovedIds.delete(id);
	modalState.fileUploadAbortControllers.delete(id);
	modalState.fileUploadActiveContexts.delete(id);
};

const removeUploadItemById = (itemOrId) => {
	const id = getUploadItemId(itemOrId);
	if (!id) {
		return;
	}
	modalState.fileUploadItems = modalState.fileUploadItems.filter((item) => item.id !== id);
	forgetUploadItem(id);
};

const modalState = {
    fileCreateModalCloseTimer: null,
    currentFileCreateType: 'file',
    fileUploadItems: [],
    fileUploadItemById: new Map(),
    fileUploadDropzoneActive: false,
    fileUploadAwaitConfirm: false,
    fileUploadLocked: false,
    fileUploadDomUpdateTimer: null,
    fileUploadDomPendingIds: new Set(),
	fileUploadRowById: new Map(),
	fileUploadDoneClearTimers: new Map(),
    fileUploadStats: {
        total: 0,
        success: 0,
        failed: 0,
    },
    fileUploadRemovedIds: new Set(),
    fileUploadActiveContexts: new Map(),
    fileUploadAbortControllers: new Map(),
    fileUploadRunToken: 0,
    fileUploadChunkSize: 9 * 1024 * 1024,
    fileUploadConcurrency: 4,
    fileUploadChunkRetryCount: 7,
    fileUploadChunkRetryDelay: 500,
    onApplyFileList: null,
    onRequestReload: null,
	getCurrentDir: null,
    isBound: false,
};

const getRecentNameStorageKey = (type) => type === 'dir' ? RECENT_DIR_NAMES_KEY : RECENT_FILE_NAMES_KEY;

const readRecentCreatedNames = (type) => {
	try {
		const raw = localStorage.getItem(getRecentNameStorageKey(type));
		const parsed = JSON.parse(raw || '[]');
		if (!Array.isArray(parsed)) {
			return [];
		}
		const names = [];
		const seen = new Set();
		for (const item of parsed) {
			const name = String(item || '').trim();
			if (!name || seen.has(name)) {
				continue;
			}
			seen.add(name);
			names.push(name);
			if (names.length >= FILE_CREATE_RECENT_LIMIT) {
				break;
			}
		}
		return names;
	} catch (error) {
		console.error('[控制台页] 读取最近创建名称失败:', error);
		return [];
	}
};

const writeRecentCreatedNames = (type, names) => {
	try {
		localStorage.setItem(getRecentNameStorageKey(type), JSON.stringify(names));
	} catch (error) {
		console.error('[控制台页] 保存最近创建名称失败:', error);
	}
};

const rememberCreatedName = (type, name) => {
	const value = String(name || '').trim();
	if (!value) {
		return;
	}
	const names = readRecentCreatedNames(type).filter((item) => item !== value);
	names.unshift(value);
	writeRecentCreatedNames(type, names.slice(0, FILE_CREATE_RECENT_LIMIT));
};

const buildRecentCreatedNameNode = (type, name) => {
	const button = document.createElement('button');
	button.type = 'button';
	button.className = 'file-create-recent-item';
	button.dataset.type = type;
	button.dataset.name = name;
	const code = document.createElement('code');
	code.textContent = name;
	button.appendChild(code);
	return button;
};

const renderRecentCreatedNames = (type) => {
	const container = type === 'dir' ? dom.fileCreateRecentDirs : dom.fileCreateRecentFiles;
	if (!container) {
		return;
	}
	const names = readRecentCreatedNames(type);
	container.replaceChildren(...names.map((name) => buildRecentCreatedNameNode(type, name)));
	container.classList.toggle('hidden', names.length === 0);
};

const renderRecentCreatedNameLists = () => {
	renderRecentCreatedNames('file');
	renderRecentCreatedNames('dir');
};

const fillCreateNameFromRecent = (type, name) => {
	const input = type === 'dir' ? dom.fileCreateDirName : dom.fileCreateName;
	if (!input) {
		return;
	}
	input.value = name;
	input.focus();
	input.select();
};

const getCurrentDir = () => {
	if (typeof modalState.getCurrentDir === 'function') {
		return String(modalState.getCurrentDir() || '');
	}
	return '';
};

const renderFileCreateTargetDir = () => {
	if (!dom.fileCreateTargetDir) {
		return;
	}
	const dir = getCurrentDir().trim();
	dom.fileCreateTargetDir.innerText = dir ? `./${dir}/` : './';
};

const renderFileCreatePage = () => {
    const type = modalState.currentFileCreateType || 'file';
    dom.fileCreateTypeFile?.classList.toggle('active', type === 'file');
    dom.fileCreateTypeDir?.classList.toggle('active', type === 'dir');
    dom.fileCreateTypeUpload?.classList.toggle('active', type === 'upload');
    dom.fileCreatePageFile?.classList.toggle('active', type === 'file');
    dom.fileCreatePageDir?.classList.toggle('active', type === 'dir');
    dom.fileCreatePageUpload?.classList.toggle('active', type === 'upload');
    if (dom.fileCreateSubmit) {
        if (type === 'upload') {
            dom.fileCreateSubmit.innerText = modalState.fileUploadAwaitConfirm ? 'CONFIRM' : 'UPLOAD';
        } else {
            dom.fileCreateSubmit.innerText = 'CREATE';
        }
    }
    if (dom.fileCreateName) {
        dom.fileCreateName.required = type === 'file';
    }
    if (dom.fileCreateDirName) {
        dom.fileCreateDirName.required = type === 'dir';
    }
	renderRecentCreatedNameLists();
};

const renderFileUploadSummary = () => {
    if (dom.fileUploadSummary) {
        const successCount = Math.max(0, modalState.fileUploadStats.success || 0);
        const failedCount = modalState.fileUploadItems.filter((item) => item.status === 'FAILED').length;
        const total = Math.max(0, Number(modalState.fileUploadStats.total || 0)) || modalState.fileUploadItems.length;
        const summaryText = renderFileUploadSummaryText({ success: successCount, total, failed: failedCount });
        dom.fileUploadSummary.innerText = summaryText;
        dom.fileUploadSummary.parentElement?.classList.toggle('hidden', !summaryText);
    }
};

const renderFileUploadList = () => {
    renderFileUploadSummary();
    if (!dom.fileUploadList) return;
	if (!modalState.fileUploadItems.length) {
		dom.fileUploadList.replaceChildren();
		dom.fileUploadList.classList.add('hidden');
		modalState.fileUploadRowById.clear();
		return;
	}
	dom.fileUploadList.classList.remove('hidden');

	dom.fileUploadList.replaceChildren(...modalState.fileUploadItems.map(buildFileUploadItemNode));

	// Refresh row map after full re-render.
	modalState.fileUploadRowById.clear();
	const rows = dom.fileUploadList.querySelectorAll('.file-upload-item');
	for (let i = 0; i < rows.length; i += 1) {
		const row = rows[i];
		const id = String(row?.dataset?.id || '');
		if (id) {
			modalState.fileUploadRowById.set(id, row);
		}
	}
};

const pruneFileUploadListIfEmpty = () => {
	if (!dom.fileUploadList || modalState.fileUploadItems.length > 0) {
		return;
	}
	dom.fileUploadList.replaceChildren();
	dom.fileUploadList.classList.add('hidden');
	modalState.fileUploadRowById.clear();
};

const updateFileUploadDropzoneState = () => {
    dom.fileUploadDropzone?.classList.toggle('dragover', !!modalState.fileUploadDropzoneActive);
    dom.fileUploadDropzone?.classList.toggle('compact', modalState.fileUploadItems.length > 0);
    dom.fileUploadDropzone?.classList.toggle('locked', !!modalState.fileUploadLocked);
    dom.fileUploadOverwrite?.closest?.('.file-upload-overwrite')?.classList.toggle('locked', !!modalState.fileUploadLocked);
    if (dom.fileUploadDropzone) {
        dom.fileUploadDropzone.disabled = !!modalState.fileUploadLocked;
    }
    if (dom.fileUploadOverwrite) {
        dom.fileUploadOverwrite.disabled = !!modalState.fileUploadLocked;
    }
};

const setFileUploadLocked = (locked) => {
    modalState.fileUploadLocked = !!locked;
    updateFileUploadDropzoneState();
};

const clearFileUploadDomUpdateTimer = () => {
    if (modalState.fileUploadDomUpdateTimer) {
        window.clearTimeout(modalState.fileUploadDomUpdateTimer);
        modalState.fileUploadDomUpdateTimer = null;
    }
    modalState.fileUploadDomPendingIds.clear();
};

const clearFileUploadDoneClearTimers = () => {
	if (!modalState.fileUploadDoneClearTimers?.size) {
		return;
	}
	modalState.fileUploadDoneClearTimers.forEach((timer) => {
		try {
			window.clearTimeout(timer);
		} catch (e) {
			// ignore
		}
	});
	modalState.fileUploadDoneClearTimers.clear();
};

const clearFileUploadDoneClearTimer = (id) => {
	const timer = modalState.fileUploadDoneClearTimers.get(id);
	if (!timer) {
		return;
	}
	try {
		window.clearTimeout(timer);
	} catch (e) {
		// ignore
	}
	modalState.fileUploadDoneClearTimers.delete(id);
};

const findFileUploadItemRow = (id) => {
    if (!dom.fileUploadList) return null;
    const targetId = String(id || '');
	const cached = modalState.fileUploadRowById.get(targetId);
	if (cached) {
		return cached;
	}
    const rows = dom.fileUploadList.querySelectorAll('.file-upload-item');
    for (let i = 0; i < rows.length; i += 1) {
        const row = rows[i];
        if ((row?.dataset?.id || '') === targetId) {
			modalState.fileUploadRowById.set(targetId, row);
            return row;
        }
    }
    return null;
};

const removeFileUploadItemRow = (id) => {
    const row = findFileUploadItemRow(id);
    if (!row) {
        return false;
    }
    if (typeof row.remove === 'function') {
        row.remove();
		modalState.fileUploadRowById.delete(String(id || ''));
        return true;
    }
    row.parentNode?.removeChild?.(row);
	modalState.fileUploadRowById.delete(String(id || ''));
    return true;
};

const applyFileUploadItemDom = (item) => {
    if (!item) return;
    const row = findFileUploadItemRow(item.id);
    if (!row) return;
	applyFileUploadItemView(row, item);
};

const queueFileUploadDomUpdate = (id) => {
    if (!id) return;
    modalState.fileUploadDomPendingIds.add(id);
    if (modalState.fileUploadDomUpdateTimer) {
        return;
    }
    modalState.fileUploadDomUpdateTimer = window.setTimeout(() => {
        modalState.fileUploadDomUpdateTimer = null;
        const ids = Array.from(modalState.fileUploadDomPendingIds);
        modalState.fileUploadDomPendingIds.clear();

        ids.forEach((pendingId) => {
            const item = modalState.fileUploadItemById.get(pendingId);
            if (!item) {
                return;
            }
            if (item.status !== 'UPLOADING' && item.status !== 'MERGING') {
                return;
            }
            applyFileUploadItemDom(item);
        });
        renderFileUploadSummary();
	}, DEFAULT_UI_REFRESH_INTERVAL_MS);
};

const setFileUploadItems = (items) => {
    clearFileUploadDoneClearTimers();
    modalState.fileUploadRunToken += 1;
    modalState.fileUploadRemovedIds.clear();
    modalState.fileUploadItems = Array.isArray(items) ? items : [];
	modalState.fileUploadItemById.clear();
	modalState.fileUploadItems.forEach(rememberUploadItem);
    modalState.fileUploadStats.total = modalState.fileUploadItems.length;
    modalState.fileUploadStats.success = 0;
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const resetFileUploadState = () => {
    clearFileUploadDomUpdateTimer();
	clearFileUploadDoneClearTimers();
	modalState.fileUploadRunToken += 1;

	const activeContexts = Array.from(modalState.fileUploadActiveContexts?.values?.() || []);
	const orphanControllers = Array.from(modalState.fileUploadAbortControllers?.entries?.() || [])
		.filter(([id]) => !modalState.fileUploadActiveContexts.has(id));

    // 终止所有仍在进行的上传
	orphanControllers.forEach(([, controller]) => {
		try {
			controller?.abort?.();
		} catch (e) {
			// ignore
		}
	});
	activeContexts.forEach((ctx) => {
		void cleanupFileUploadContext(ctx, {
			abortController: true,
			abortRemote: true,
		});
	});
	modalState.fileUploadAbortControllers.clear();
    modalState.fileUploadActiveContexts?.clear?.();
    modalState.fileUploadRemovedIds?.clear?.();
    modalState.fileUploadItems = [];
	modalState.fileUploadItemById.clear();
	modalState.fileUploadRowById.clear();
    modalState.fileUploadDropzoneActive = false;
    modalState.fileUploadAwaitConfirm = false;
    modalState.fileUploadLocked = false;
    modalState.fileUploadStats.total = 0;
    modalState.fileUploadStats.success = 0;
    if (dom.fileUploadOverwrite) {
        dom.fileUploadOverwrite.checked = false;
    }
    if (dom.fileUploadInput) {
        dom.fileUploadInput.value = '';
    }
    renderFileCreatePage();
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const cancelFileUploadItem = async (id) => {
    if (!id) {
        return;
    }
    modalState.fileUploadRemovedIds.add(id);
    const ctx = modalState.fileUploadActiveContexts.get(id);
    if (ctx) {
        ctx.removed = true;
    }
	if (ctx) {
		void cleanupFileUploadContext(ctx, {
			abortController: true,
			abortRemote: true,
		});
		return;
	}

	const controller = modalState.fileUploadAbortControllers.get(id);
	if (controller) {
		try {
			controller.abort();
		} catch (e) {
			// ignore
		}
	}
	modalState.fileUploadAbortControllers.delete(id);
    modalState.fileUploadActiveContexts.delete(id);
};

const abortFileUploadSession = async (instanceName, uploadId) => {
	if (!instanceName || !uploadId) {
		return;
	}
	try {
		await abortFileUpload(instanceName, uploadId);
	} catch (e) {
		console.warn(`[控制台页] 清理上传会话失败: ${uploadId}`, e);
	}
};

const cleanupFileUploadContext = async (ctx, options = {}) => {
	if (!ctx) {
		return;
	}
	const abortController = options.abortController === true;
	const abortRemote = options.abortRemote === true;
	const itemId = ctx.item?.id;
	if (abortController && !ctx.controllerAborted) {
		ctx.controllerAborted = true;
		try {
			ctx.controller?.abort?.();
		} catch (e) {
			// ignore
		}
	}
	if (itemId) {
		modalState.fileUploadAbortControllers.delete(itemId);
		modalState.fileUploadActiveContexts.delete(itemId);
	}
	if (!abortRemote || ctx.remoteAbortRequested || ctx.completed || ctx.completeRequested) {
		return;
	}
	ctx.remoteAbortRequested = true;
	await abortFileUploadSession(ctx.instanceName, ctx.uploadId);
};

const isUploadItemCanceled = (item, runToken) => {
	if (!item) {
		return true;
	}
	if (runToken !== modalState.fileUploadRunToken) {
		return true;
	}
	if (!modalState.fileUploadItemById.has(item.id)) {
		return true;
	}
	return modalState.fileUploadRemovedIds.has(item.id);
};

const setFileUploadDropzoneActive = (active) => {
    if (modalState.fileUploadLocked) {
        modalState.fileUploadDropzoneActive = false;
        updateFileUploadDropzoneState();
        return;
    }
    modalState.fileUploadDropzoneActive = !!active;
    updateFileUploadDropzoneState();
};

const createFileEntry = async (type, name, content = '', overwrite = false) => {
	const instanceName = state.currentInstanceName;
	if (!instanceName) {
		return null;
	}

	if (!name) {
		await showAlert('名称不能为空', { title: 'INPUT' });
		return null;
	}

	const result = type === 'dir'
		? await createDirectory(instanceName, getCurrentDir(), name)
		: await createTextFileAdaptive(instanceName, getCurrentDir(), name, content, overwrite);
	if (!result?.ok || !result.data) {
		if (result?.unauthorized) {
			return null;
		}
		const actionText = type === 'dir' ? '创建目录' : '创建文件';
		await showAlert(`${actionText}失败: ${result?.error || '操作失败'}`, { title: 'ERROR', tone: 'danger' });
		return null;
	}
	if (typeof modalState.onApplyFileList === 'function') {
		modalState.onApplyFileList(result.data);
	}

	return result.data;
};

const setFileUploadProgress = (id, payload = {}) => {
	const item = modalState.fileUploadItemById.get(id) || null;
	if (!item) {
		return;
	}
	const prevStatus = item.status || '';
	const nextStatus = typeof payload.status === 'string' ? payload.status : (item.status || '');
	const loaded = typeof payload.loaded === 'number' ? payload.loaded : item.loaded;
	const progress = typeof payload.progress === 'number' ? payload.progress : item.progress;
	const errorMessage = typeof payload.errorMessage === 'string' ? payload.errorMessage : item.errorMessage;
	Object.assign(item, payload, {
		loaded,
		progress,
		errorMessage,
		status: nextStatus,
	});

	if (nextStatus === 'FAILED' || nextStatus === 'DONE') {
		modalState.fileUploadAbortControllers.delete(id);
		modalState.fileUploadActiveContexts.delete(id);
	}

	if (nextStatus === 'DONE') {
		// 只有完全成功的才自动删除；FAILED 不删除
		if (prevStatus !== 'DONE') {
			modalState.fileUploadStats.success = (modalState.fileUploadStats.success || 0) + 1;
		}
		clearFileUploadDoneClearTimer(id);
		applyFileUploadItemDom(item);
		renderFileUploadSummary();
		modalState.fileUploadDoneClearTimers.set(id, window.setTimeout(() => {
			modalState.fileUploadDoneClearTimers.delete(id);
			const current = modalState.fileUploadItemById.get(id) || null;
			if (!current || current.status !== 'DONE') {
				return;
			}
			removeUploadItemById(id);
			const removed = removeFileUploadItemRow(id);
			if (!removed) {
				// DOM 可能被外部改动，兜底做一次结构渲染以同步视图
				renderFileUploadList();
			}
			renderFileUploadSummary();
			updateFileUploadDropzoneState();
			pruneFileUploadListIfEmpty();
		}, 500));
		return;
	}

	if (nextStatus !== 'UPLOADING' && nextStatus !== 'MERGING') {
		// 状态切换(等待/失败等)直接局部更新一次，避免等待统一刷新间隔
		applyFileUploadItemDom(item);
		renderFileUploadSummary();
		return;
	}
	queueFileUploadDomUpdate(id);
};

const updateUploadItemProgress = (item, loaded, status) => {
    const total = item.size || 0;
    const safeLoaded = Math.max(0, Math.min(total, loaded));
    setFileUploadProgress(item.id, {
        loaded: safeLoaded,
        progress: total > 0 ? safeLoaded / total * 100 : 100,
        status,
        errorMessage: '',
    });
};

const waitFileUploadRetryDelay = (delay) => new Promise((resolve) => {
    window.setTimeout(resolve, Math.max(0, delay || 0));
});

const normalizeUploadRelativePath = (path) => String(path || '')
	.replaceAll('\\', '/')
	.split('/')
	.map((part) => part.trim())
	.filter((part) => part && part !== '.' && part !== '..')
	.join('/');

const getUploadPathParts = (path) => normalizeUploadRelativePath(path).split('/').filter(Boolean);

const getUploadRootName = (path, fallback) => {
	const parts = getUploadPathParts(path);
	return parts[0] || String(fallback || '').trim() || 'folder';
};

const compareUploadPath = (a, b) => {
	const left = String(a || '');
	const right = String(b || '');
	if (left < right) {
		return -1;
	}
	if (left > right) {
		return 1;
	}
	return 0;
};

const getUploadPathWithoutRoot = (path) => {
	const parts = getUploadPathParts(path);
	return parts.slice(1).join('/');
};

const getUploadFileSignature = (file) => [
	String(file?.name || ''),
	String(file?.size || 0),
	String(file?.lastModified || 0),
].join('\u0000');

const isLikelyDroppedDirectoryPlaceholder = (file) => {
	return !!file && Number(file.size || 0) === 0 && !String(file.type || '').trim();
};

const isLikelyTopLevelDirectoryPlaceholder = (file, parentPath = '') => {
	return !String(parentPath || '').trim() && isLikelyDroppedDirectoryPlaceholder(file);
};

const createPlainUploadItem = (file, index) => ({
	id: `${Date.now()}-${index}-${file.name}`,
	file,
	kind: 'file',
	name: file.name,
	size: file.size,
	loaded: 0,
	progress: 0,
	status: 'WAITING',
	errorMessage: '',
});

const createDirectoryUploadItem = (name, pickedFiles, pickedDirs, index) => {
	const files = Array.from(pickedFiles || [])
		.map((entry) => ({
			file: entry.file,
			path: normalizeUploadRelativePath(entry.relativePath || entry.file?.name || ''),
		}))
		.filter((entry) => entry.file && entry.path)
		.sort((a, b) => compareUploadPath(a.path, b.path));
	const items = files.map((entry, fileIndex) => createPlainUploadItem(entry.file, `${index}-${fileIndex}`));
	items.forEach((item, fileIndex) => {
		const relativePath = files[fileIndex]?.path || item.name;
		const parts = getUploadPathParts(relativePath);
		item.path = parts.slice(0, -1).join('/');
		item.name = parts[parts.length - 1] || item.name;
	});
	const emptyDirs = Array.from(pickedDirs || [])
		.map((path) => normalizeUploadRelativePath(path))
		.filter(Boolean)
		.filter((path) => !files.some((entry) => entry.path === path || entry.path.startsWith(`${path}/`)));
	emptyDirs.forEach((path, dirIndex) => {
		items.push({
			id: `${Date.now()}-${index}-dir-${dirIndex}-${path}`,
			kind: 'empty-directory',
			name: path,
			size: 0,
			loaded: 0,
			progress: 0,
			status: 'WAITING',
			errorMessage: '',
		});
	});
	return items;
};

const buildUploadItemsFromPickedEntries = (entries, dirs = []) => {
	const groups = new Map();
	const rootDirSeen = new Set();
	Array.from(entries || []).forEach((entry) => {
		if (!entry?.file) {
			return;
		}
		const relativePath = normalizeUploadRelativePath(entry.relativePath || entry.file.webkitRelativePath || entry.file.name);
		const rootName = getUploadRootName(relativePath, entry.file.name);
		if (entry.maybeDirectoryPlaceholder) {
			rootDirSeen.add(rootName);
			const key = `dir:${rootName}`;
			if (!groups.has(key)) {
				groups.set(key, { type: 'directory', name: rootName, files: [], dirs: [] });
			}
			groups.get(key).dirs.push(rootName);
			return;
		}
		if (!entry.isDirectory && !entry.file.webkitRelativePath) {
			const key = `file:${entry.file.name}:${groups.size}`;
			groups.set(key, { type: 'file', file: entry.file });
			return;
		}
		rootDirSeen.add(rootName);
		const key = `dir:${rootName}`;
		if (!groups.has(key)) {
			groups.set(key, { type: 'directory', name: rootName, files: [], dirs: [] });
		}
		groups.get(key).files.push({ file: entry.file, relativePath });
	});
	Array.from(dirs || []).forEach((path) => {
		const relativePath = normalizeUploadRelativePath(path);
		const rootName = getUploadRootName(relativePath, 'folder');
		const key = `dir:${rootName}`;
		if (relativePath === rootName) {
			rootDirSeen.add(rootName);
		}
		if (!groups.has(key)) {
			groups.set(key, { type: 'directory', name: rootName, files: [], dirs: [] });
		}
		groups.get(key).dirs.push(relativePath);
	});
	rootDirSeen.forEach((rootName) => {
		const key = `dir:${rootName}`;
		if (!groups.has(key)) {
			groups.set(key, { type: 'directory', name: rootName, files: [], dirs: [] });
		}
	});
	return Array.from(groups.values()).flatMap((group, index) => {
		if (group.type === 'file') {
			return createPlainUploadItem(group.file, index);
		}
		return createDirectoryUploadItem(group.name, group.files, group.dirs, index);
	});
};

const readEntryFile = (entry) => new Promise((resolve, reject) => entry.file(resolve, reject));

const readAllDirectoryEntries = async (reader) => {
	const entries = [];
	while (true) {
		const batch = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
		if (!batch.length) {
			break;
		}
		entries.push(...batch);
	}
	return entries;
};

const readDroppedEntry = async (entry, parentPath = '') => {
	const path = parentPath ? `${parentPath}/${entry.name}` : entry.name;
	if (entry.isFile) {
		const file = await readEntryFile(entry);
		return {
			files: [{
				file,
				relativePath: path,
				isDirectory: !!parentPath,
				maybeDirectoryPlaceholder: isLikelyTopLevelDirectoryPlaceholder(file, parentPath),
			}],
			dirs: [],
		};
	}
	if (entry.isDirectory) {
		const children = await readAllDirectoryEntries(entry.createReader());
		const files = [];
		const dirs = [path];
		for (const child of children) {
			const childResult = await readDroppedEntry(child, path);
			files.push(...childResult.files);
			dirs.push(...childResult.dirs);
		}
		return { files, dirs };
	}
	return { files: [], dirs: [] };
};

const readDroppedFileSystemHandle = async (handle, parentPath = '') => {
	if (!handle) {
		return { files: [], dirs: [] };
	}
	const path = parentPath ? `${parentPath}/${handle.name}` : handle.name;
	if (handle.kind === 'file') {
		const file = await handle.getFile();
		return {
			files: [{
				file,
				relativePath: path,
				isDirectory: !!parentPath,
				maybeDirectoryPlaceholder: isLikelyTopLevelDirectoryPlaceholder(file, parentPath),
			}],
			dirs: [],
		};
	}
	if (handle.kind === 'directory') {
		const files = [];
		const dirs = [path];
		for await (const child of handle.values()) {
			const childResult = await readDroppedFileSystemHandle(child, path);
			files.push(...childResult.files);
			dirs.push(...childResult.dirs);
		}
		return { files, dirs };
	}
	return { files: [], dirs: [] };
};

const readDataTransferItemHandle = async (item) => {
	const getAsFileSystemHandle = item?.getAsFileSystemHandle;
	if (typeof getAsFileSystemHandle !== 'function') {
		return null;
	}
	return await getAsFileSystemHandle.call(item);
};

const readDroppedUploadItems = async (dataTransfer) => {
	const items = Array.from(dataTransfer?.items || []);
	const fallbackFiles = Array.from(dataTransfer?.files || []);
	if (!items.length) {
		return buildUploadItemsFromPickedEntries(fallbackFiles.map((file) => ({ file })));
	}
	const droppedItems = [];
	let hasTopLevelFile = false;
	let hasTopLevelDirectory = false;
	for (const item of items) {
		if (item.kind !== 'file') {
			continue;
		}
		const getAsEntry = item.getAsEntry || item.webkitGetAsEntry;
		const entry = typeof getAsEntry === 'function' ? getAsEntry.call(item) : null;
		const file = entry ? null : item.getAsFile?.();
		const isDirectoryPlaceholder = !entry && isLikelyDroppedDirectoryPlaceholder(file);
		if (entry?.isDirectory || isDirectoryPlaceholder) {
			hasTopLevelDirectory = true;
		} else if (entry?.isFile || file) {
			hasTopLevelFile = true;
		}
		droppedItems.push({ item, entry, file, isDirectoryPlaceholder });
	}
	const shouldSkipDirectories = hasTopLevelFile && hasTopLevelDirectory;
	const files = [];
	const dirs = [];
	for (const dropped of droppedItems) {
		if (shouldSkipDirectories && (dropped.entry?.isDirectory || dropped.isDirectoryPlaceholder)) {
			continue;
		}
		if (dropped.entry) {
			const result = await readDroppedEntry(dropped.entry);
			files.push(...result.files);
			dirs.push(...result.dirs);
			continue;
		}
		try {
			const handle = await readDataTransferItemHandle(dropped.item);
			if (handle) {
				if (shouldSkipDirectories && handle.kind === 'directory') {
					continue;
				}
				const result = await readDroppedFileSystemHandle(handle);
				files.push(...result.files);
				dirs.push(...result.dirs);
				continue;
			}
		} catch (error) {
			console.warn('[控制台页] 读取拖拽文件系统句柄失败, 将回退到 DataTransferItem:', error);
		}
		if (dropped.file) {
			if (shouldSkipDirectories && dropped.isDirectoryPlaceholder) {
				continue;
			}
			files.push({ file: dropped.file, maybeDirectoryPlaceholder: dropped.isDirectoryPlaceholder });
		}
	}
	if (!dirs.length && fallbackFiles.length > files.length) {
		const seen = new Set(files.map((entry) => getUploadFileSignature(entry.file)));
		fallbackFiles.forEach((file) => {
			const signature = getUploadFileSignature(file);
			if (seen.has(signature)) {
				return;
			}
			seen.add(signature);
			files.push({ file });
		});
	}
	return buildUploadItemsFromPickedEntries(files, dirs);
};

const uploadFileChunkWithRetry = async (instanceName, uploadId, index, chunk, onProgress, options = {}) => {
    const maxAttempts = Math.max(1, modalState.fileUploadChunkRetryCount || 1);
    let lastError = null;

    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
        try {
			await uploadFileChunk(instanceName, uploadId, index, chunk, onProgress, options);
            return;
        } catch (error) {
            lastError = error;
            if (error?.name === 'AbortError') {
                throw error;
            }
            if (attempt >= maxAttempts) {
                break;
            }
            await waitFileUploadRetryDelay(modalState.fileUploadChunkRetryDelay);
        }
    }

    throw lastError || new Error(`Chunk ${index} upload failed`);
};

const initFileUploadContext = async (instanceName, currentPath, item, overwrite, options = {}) => {
	const runToken = Number(options.runToken) || modalState.fileUploadRunToken;
	const file = item.file;
	const uploadSize = file.size;
	const chunkSize = uploadSize > modalState.fileUploadChunkSize ? modalState.fileUploadChunkSize : Math.max(uploadSize, 1);
	const chunkCount = Math.max(1, Math.ceil(file.size / chunkSize));
	const initResult = await initFileUpload(instanceName, {
		path: [currentPath, item.path].filter(Boolean).join('/'),
		name: item.name,
		size: uploadSize,
		chunk_size: chunkSize,
		chunk_count: chunkCount,
		overwrite,
	});
	if (!initResult?.upload_id) {
		throw new Error('UPLOAD INIT FAILED');
	}

	const chunkProgress = new Array(chunkCount).fill(0);
	const ctx = {
		item,
		file,
		instanceName,
		runToken,
		uploadId: initResult.upload_id,
		chunkSize,
		chunkCount,
		chunkProgress,
		totalLoaded: 0,
		nextChunkIndex: 0,
		refreshProgress: null,
		failed: false,
		removed: false,
		controller: null,
		controllerAborted: false,
		merging: false,
		completeRequested: false,
		remoteAbortRequested: false,
		completed: false,
	};

	ctx.refreshProgress = (status) => {
		updateUploadItemProgress(item, ctx.totalLoaded || 0, status);
	};

	ctx.removed = isUploadItemCanceled(item, runToken);
	ctx.controller = new AbortController();
	modalState.fileUploadAbortControllers.set(item.id, ctx.controller);
	modalState.fileUploadActiveContexts.set(item.id, ctx);

	if (ctx.removed) {
		// init 已经创建了后端临时文件，立即通知后端清理
		void cleanupFileUploadContext(ctx, {
			abortController: true,
			abortRemote: true,
		});
	}

	ctx.uploadChunk = async (index) => {
		if (ctx.failed || ctx.removed) {
			return;
		}
		const chunkFile = ctx.file;
		const localIndex = index;
		if (!chunkFile) {
			throw new Error(`Chunk ${index} source missing`);
		}
		const start = localIndex * ctx.chunkSize;
		const end = Math.min(chunkFile.size, start + ctx.chunkSize);
		const chunk = chunkFile.slice(start, end);
		await uploadFileChunkWithRetry(instanceName, ctx.uploadId, index, chunk, (loaded) => {
			const safeLoaded = Math.max(0, Math.min(chunk.size, loaded || 0));
			const prevLoaded = ctx.chunkProgress[index] || 0;
			const delta = safeLoaded - prevLoaded;
			ctx.chunkProgress[index] = safeLoaded;
			ctx.totalLoaded = Math.max(0, (ctx.totalLoaded || 0) + delta);
			ctx.refreshProgress('UPLOADING');
		}, { signal: ctx.controller.signal });
		const prevLoaded = ctx.chunkProgress[index] || 0;
		if (prevLoaded !== chunk.size) {
			ctx.chunkProgress[index] = chunk.size;
			ctx.totalLoaded = Math.max(0, (ctx.totalLoaded || 0) + (chunk.size - prevLoaded));
		}
		ctx.refreshProgress('UPLOADING');
	};

	ctx.refreshProgress('UPLOADING');
	return ctx;
};

const uploadFileGroupChunks = async (contexts, limit, options = {}) => {
	const limitValue = Math.max(1, limit || 1);
	const inFlight = new Map();
	const ctxInFlightCount = new Map();
	const completeTasks = new Set();
	const onContextComplete = typeof options.onContextComplete === 'function'
		? options.onContextComplete
		: null;
	const getNextContext = typeof options.getNextContext === 'function'
		? options.getNextContext
		: null;

	const canScheduleCtx = (ctx) => {
		if (!ctx || ctx.failed || ctx.removed) {
			return false;
		}
		return ctx.nextChunkIndex < ctx.chunkCount;
	};

	const maybeCompleteContext = (ctx) => {
		if (!ctx || ctx.failed || ctx.removed) {
			return;
		}
		if (!onContextComplete) {
			return;
		}
		const pending = ctxInFlightCount.get(ctx) || 0;
		if (pending > 0) {
			return;
		}
		if (ctx.nextChunkIndex < ctx.chunkCount) {
			return;
		}
		if (ctx.merging) {
			return;
		}
		ctx.merging = true;
		const task = Promise.resolve()
			.then(() => onContextComplete(ctx))
			.catch((e) => {
				// ignore: caller should handle UI state
				void e;
			});
		completeTasks.add(task);
		task.finally(() => completeTasks.delete(task));
	};

	const scheduleChunkForCtx = (ctx) => {
		if (!canScheduleCtx(ctx)) {
			maybeCompleteContext(ctx);
			return false;
		}
		const idx = ctx.nextChunkIndex;
		ctx.nextChunkIndex += 1;
		const p = ctx.uploadChunk(idx);
		inFlight.set(p, ctx);
		ctxInFlightCount.set(ctx, (ctxInFlightCount.get(ctx) || 0) + 1);
		return true;
	};

	const countActiveContexts = () => {
		let count = 0;
		for (let i = 0; i < contexts.length; i += 1) {
			const ctx = contexts[i];
			if (!ctx || ctx.failed || ctx.removed) {
				continue;
			}
			const pending = ctxInFlightCount.get(ctx) || 0;
			if (pending > 0 || ctx.nextChunkIndex < ctx.chunkCount) {
				count += 1;
			}
		}
		return count;
	};

	const fillContextSlots = async () => {
		if (!getNextContext) {
			return;
		}
		while (countActiveContexts() < limitValue) {
			let next = null;
			try {
				next = await getNextContext();
			} catch (e) {
				// ignore
				next = null;
			}
			if (!next) {
				break;
			}
			contexts.push(next);
		}
	};

	const fillSlots = async () => {
		await fillContextSlots();
		// 1) 优先让不同文件各占一个并发槽位
		for (let i = 0; i < contexts.length && inFlight.size < limitValue; i += 1) {
			const ctx = contexts[i];
			if (!canScheduleCtx(ctx)) {
				maybeCompleteContext(ctx);
				continue;
			}
			if ((ctxInFlightCount.get(ctx) || 0) > 0) {
				continue;
			}
			scheduleChunkForCtx(ctx);
		}

		// 2) 文件数不足时，再均匀分配额外分片（选择 in-flight 最少的 ctx）
		while (inFlight.size < limitValue) {
			let bestCtx = null;
			let bestCount = Infinity;
			for (let i = 0; i < contexts.length; i += 1) {
				const ctx = contexts[i];
				if (!canScheduleCtx(ctx)) {
					maybeCompleteContext(ctx);
					continue;
				}
				const c = ctxInFlightCount.get(ctx) || 0;
				if (c < bestCount) {
					bestCount = c;
					bestCtx = ctx;
				}
			}
			if (!bestCtx) {
				break;
			}
			scheduleChunkForCtx(bestCtx);
		}
	};

	await fillSlots();
	while (inFlight.size > 0) {
		const settled = await Promise.race(Array.from(inFlight.keys()).map((p) => p.then(
			() => ({ status: 'fulfilled', p }),
			(reason) => ({ status: 'rejected', p, reason }),
		)));
		const ctx = inFlight.get(settled.p);
		inFlight.delete(settled.p);
		if (!ctx) {
			continue;
		}
		ctxInFlightCount.set(ctx, Math.max(0, (ctxInFlightCount.get(ctx) || 0) - 1));

		if (settled.status === 'rejected') {
			if (!ctx.removed && !ctx.failed) {
				if (settled.reason?.name === 'AbortError') {
					ctx.failed = true;
					setFileUploadProgress(ctx.item.id, {
						loaded: ctx.item.loaded || 0,
						progress: ctx.item.progress || 0,
						status: 'FAILED',
						errorMessage: '已取消',
					});
				} else {
					ctx.failed = true;
					console.error(`[控制台页] 上传文件 ${ctx.item?.name || ''} 分片失败:`, settled.reason);
					setFileUploadProgress(ctx.item.id, {
						loaded: ctx.item.loaded || 0,
						progress: ctx.item.progress || 0,
						status: 'FAILED',
						errorMessage: getUploadErrorText(settled.reason),
					});
					await cleanupFileUploadContext(ctx, {
						abortController: true,
						abortRemote: true,
					});
				}
			}
		}

		maybeCompleteContext(ctx);
		await fillSlots();
	}

	contexts.forEach((ctx) => maybeCompleteContext(ctx));
	if (completeTasks.size > 0) {
		await Promise.allSettled(Array.from(completeTasks));
	}
};

const hasFailedFileUploads = () => modalState.fileUploadItems.some((item) => item.status === 'FAILED');

const setFileUploadAwaitConfirm = (value) => {
    modalState.fileUploadAwaitConfirm = !!value;
    renderFileCreatePage();
};

const prepareRetryFileUploads = () => {
    modalState.fileUploadRemovedIds.clear();
    modalState.fileUploadItems = modalState.fileUploadItems
        .filter((item) => item.status !== 'DONE')
        .map((item) => {
            if (item.status !== 'FAILED') {
                return item;
            }
            return {
                ...item,
                loaded: 0,
                progress: 0,
                status: 'WAITING',
                errorMessage: '',
            };
        });
	modalState.fileUploadItemById.clear();
	modalState.fileUploadItems.forEach((item) => {
		if (item && item.id) {
			modalState.fileUploadItemById.set(item.id, item);
		}
	});
    modalState.fileUploadStats.total = modalState.fileUploadItems.length;
    modalState.fileUploadStats.success = 0;
    modalState.fileUploadAwaitConfirm = false;
    renderFileCreatePage();
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const uploadSelectedFiles = async (overwrite) => {
	const instanceName = state.currentInstanceName;
	const currentPath = getCurrentDir();
	const items = modalState.fileUploadItems;
	if (!instanceName || !items.length) {
		await showAlert('请选择至少一个文件', { title: 'INPUT' });
		return false;
	}

	let hasSuccess = false;
	let hasFinished = false;
	let hasDone = false;
	const limit = Math.max(1, modalState.fileUploadConcurrency || 1);
	setFileUploadLocked(true);
	try {
		items.forEach((item) => {
			updateUploadItemProgress(item, 0, 'WAITING');
		});

		let nextIndex = 0;
		const runToken = modalState.fileUploadRunToken;
		const getNextContext = async () => {
			for (; nextIndex < items.length; nextIndex += 1) {
				const item = items[nextIndex];
				if (!item) {
					continue;
				}
				if (isUploadItemCanceled(item, runToken)) {
					continue;
				}
				try {
					if (item.kind === 'empty-directory') {
						await createDirectory(instanceName, currentPath, item.name);
						hasFinished = true;
						hasSuccess = true;
						updateUploadItemProgress(item, 0, 'DONE');
						continue;
					}
					const ctx = await initFileUploadContext(instanceName, currentPath, item, overwrite, { runToken });
					if (!ctx || ctx.failed || ctx.removed || isUploadItemCanceled(item, runToken)) {
						if (ctx && !ctx.completed) {
							ctx.removed = true;
							void cleanupFileUploadContext(ctx, {
								abortController: true,
								abortRemote: true,
							});
						}
						continue;
					}
					nextIndex += 1;
					return ctx;
				} catch (e) {
					hasFinished = true;
					console.error(`[控制台页] 上传文件 ${item.name} 初始化失败:`, e);
					setFileUploadProgress(item.id, {
						loaded: item.loaded || 0,
						progress: item.progress || 0,
						status: 'FAILED',
						errorMessage: getUploadErrorText(e),
					});
				}
			}
			return null;
		};

		await uploadFileGroupChunks([], limit, {
			getNextContext,
			onContextComplete: async (ctx) => {
				if (!ctx || ctx.failed || ctx.removed) {
					return;
				}
				try {
					updateUploadItemProgress(ctx.item, ctx.item.size || ctx.file?.size || 0, 'MERGING');
					ctx.completeRequested = true;
					await completeFileUpload(instanceName, ctx.uploadId);
					hasFinished = true;
					ctx.completed = true;
					updateUploadItemProgress(ctx.item, ctx.item.size || ctx.file?.size || 0, 'DONE');
					hasSuccess = true;
					modalState.fileUploadAbortControllers.delete(ctx.item.id);
					modalState.fileUploadActiveContexts.delete(ctx.item.id);
				} catch (e) {
					if (e?.name === 'AbortError') {
						ctx.failed = true;
						setFileUploadProgress(ctx.item.id, {
							loaded: ctx.item.loaded || 0,
							progress: ctx.item.progress || 0,
							status: 'FAILED',
							errorMessage: '已取消',
						});
						await cleanupFileUploadContext(ctx, {
							abortController: true,
							abortRemote: true,
						});
						return;
					}
					hasFinished = true;
					ctx.failed = true;
					console.error(`[控制台页] 上传文件 ${ctx.item?.name || ''} 合并失败:`, e);
					setFileUploadProgress(ctx.item.id, {
						loaded: ctx.item.loaded || 0,
						progress: ctx.item.progress || 0,
						status: 'FAILED',
						errorMessage: getUploadErrorText(e),
					});
					await cleanupFileUploadContext(ctx, {
						abortController: true,
						abortRemote: true,
					});
				}
			},
		});
	} finally {
		setFileUploadLocked(false);
		trimFinishedUploadStats();
	}
    if (hasSuccess && typeof modalState.onRequestReload === 'function') {
        await modalState.onRequestReload();
    }

    // DONE 会被自动删除，所以不要依赖最终数组来判断。
    if (hasFinished) {
        hasDone = true;
    }
    return hasDone;
};

const replaceFileUploadItems = (files) => {
	const items = buildUploadItemsFromPickedEntries(Array.from(files || []).map((file) => ({
		file,
		relativePath: file.webkitRelativePath || file.name,
		isDirectory: !!file.webkitRelativePath,
	})));
    replaceFileUploadItemsWithItems(items);
};

const replaceFileUploadItemsWithItems = (items) => {
    setFileUploadAwaitConfirm(false);
    setFileUploadItems(items);
    if (dom.fileUploadInput) {
        dom.fileUploadInput.value = '';
    }
	if (dom.fileUploadDirectoryInput) {
		dom.fileUploadDirectoryInput.value = '';
	}
};

const removeFileUploadItem = (id) => {
    const target = modalState.fileUploadItems.find((item) => item.id === id) || null;
    // 不 await，保证 UI 立即响应，同时后台会异步清理/取消
    cancelFileUploadItem(id);
	clearFileUploadDoneClearTimer(id);
	removeUploadItemById(id);
    void target;
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const trimFinishedUploadStats = () => {
	renderFileUploadSummary();
};

const setFileCreateType = (type) => {
    if (type === 'dir' || type === 'upload') {
        modalState.currentFileCreateType = type;
    } else {
        modalState.currentFileCreateType = 'file';
    }
    renderFileCreatePage();
	if (type === 'dir') {
		dom.fileCreateDirName.focus();
	}
	if (type === 'file') {
		dom.fileCreateName.focus();
	}
};

const getFileCreateFormValue = () => {
    const type = modalState.currentFileCreateType || 'file';
	const fileName = truncateInputValue(dom.fileCreateName, InputValidation.limits.fileName).trim();
	const dirName = truncateInputValue(dom.fileCreateDirName, InputValidation.limits.fileName).trim();
	const content = dom.fileCreateContent ? dom.fileCreateContent.value : '';
	return {
        type,
        name: type === 'dir' ? dirName : fileName,
		content,
		overwrite: type === 'upload'
			? !!dom.fileUploadOverwrite?.checked
			: !!dom.fileCreateOverwrite?.checked,
    };
};

const resetFileCreateUploadForm = () => {
    resetFileUploadState();
    modalState.currentFileCreateType = 'upload';
    renderFileCreatePage();
};

const handleFileCreateSubmit = async (event) => {
    event.preventDefault();

	await withActionsDisabled(dom.fileCreateActions, async () => {
		const { type, name, content, overwrite } = getFileCreateFormValue();
		if (type === 'upload') {
			if (modalState.fileUploadAwaitConfirm) {
				resetFileCreateUploadForm();
				return;
			}
			if (hasFailedFileUploads()) {
				prepareRetryFileUploads();
			}
			const ok = await uploadSelectedFiles(overwrite);
			if (ok) {
				setFileUploadAwaitConfirm(!hasFailedFileUploads());
			}
			return;
		}

		if (!name) {
			await showAlert('名称不能为空', { title: 'INPUT' });
			if (type === 'dir') {
				dom.fileCreateDirName?.focus();
			} else {
				dom.fileCreateName?.focus();
			}
			return;
		}

		const data = await createFileEntry(type, name, content, overwrite);
		if (!data) {
			return;
		}
		rememberCreatedName(type, name);
		renderRecentCreatedNames(type);

		close();
	});
};

const hasActiveFileUploads = () => !!modalState.fileUploadLocked;

const cancelAllFileUploads = () => {
    const ids = modalState.fileUploadItems.map((item) => item.id);
    ids.forEach((id) => {
        // 不 await，避免阻塞关闭动画
        cancelFileUploadItem(id);
    });
};

const tryCloseWithConfirm = async () => {
    if (modalState.currentFileCreateType === 'upload' && hasActiveFileUploads()) {
        const ok = await showConfirm('正在上传中，关闭将取消所有传输。是否关闭？', {
            title: 'CONFIRM',
            okText: 'CLOSE',
            cancelText: 'CANCEL',
            tone: 'warning',
        });
        if (!ok) {
            return;
        }
        cancelAllFileUploads();
    }
    close();
};

const open = (options = {}) => {
    if (!dom.fileCreateModal || !dom.fileCreateName || !dom.fileCreateTypeFile) return;
    modalState.fileCreateModalCloseTimer = clearTimer(modalState.fileCreateModalCloseTimer);
	dom.fileCreateForm?.reset?.();
    if (dom.fileCreateContent) {
        dom.fileCreateContent.value = '';
    }
	if (dom.fileCreateOverwrite) {
		dom.fileCreateOverwrite.checked = false;
	}
	const initialType = String(options?.type || '').trim() === 'upload' ? 'upload' : 'file';
	modalState.currentFileCreateType = initialType;
    resetFileUploadState();
	modalState.currentFileCreateType = initialType;
	renderFileCreateTargetDir();
	renderRecentCreatedNameLists();
	renderFileCreatePage();
	openAnimatedModal(dom.fileCreateModal);
	if (initialType === 'upload') {
		dom.fileUploadDropzone?.focus?.();
	} else {
		dom.fileCreateName.focus();
	}
};

const openUploadWithDataTransfer = async (dataTransfer) => {
	open({ type: 'upload' });
	try {
		const items = await readDroppedUploadItems(dataTransfer);
		replaceFileUploadItemsWithItems(items);
	} catch (error) {
		console.error('[控制台页] 读取拖拽文件失败:', error);
		await showAlert(getUploadErrorText(error), { title: 'UPLOAD' });
	}
};

const close = () => {
    if (!dom.fileCreateModal) return;
    resetFileUploadState();
	modalState.fileCreateModalCloseTimer = closeAnimatedModal(dom.fileCreateModal, modalState.fileCreateModalCloseTimer, () => {
		modalState.fileCreateModalCloseTimer = null;
	});
};

const bindEvents = () => {
    if (modalState.isBound) {
        return;
    }
    modalState.isBound = true;

    if (dom.fileCreateTypeFile) {
        dom.fileCreateTypeFile.onclick = () => setFileCreateType('file');
    }
    if (dom.fileCreateTypeDir) {
        dom.fileCreateTypeDir.onclick = () => setFileCreateType('dir');
    }
    if (dom.fileCreateTypeUpload) {
        dom.fileCreateTypeUpload.onclick = () => setFileCreateType('upload');
    }
    if (dom.fileCreateClose) {
        dom.fileCreateClose.onclick = () => {
            void tryCloseWithConfirm();
        };
    }
    if (dom.fileCreateCancel) {
        dom.fileCreateCancel.onclick = () => {
            void tryCloseWithConfirm();
        };
    }
    if (dom.fileCreateForm) {
        dom.fileCreateForm.onsubmit = handleFileCreateSubmit;
    }
	if (dom.fileCreateRecentFiles) {
		dom.fileCreateRecentFiles.onclick = (event) => {
			const item = event.target.closest('.file-create-recent-item');
			if (!item) {
				return;
			}
			fillCreateNameFromRecent('file', item.dataset.name || '');
		};
	}
	if (dom.fileCreateRecentDirs) {
		dom.fileCreateRecentDirs.onclick = (event) => {
			const item = event.target.closest('.file-create-recent-item');
			if (!item) {
				return;
			}
			fillCreateNameFromRecent('dir', item.dataset.name || '');
		};
	}
    if (dom.fileUploadDropzone) {
        dom.fileUploadDropzone.onclick = (event) => {
            if (modalState.fileUploadLocked) {
                return;
            }
			if (event?.shiftKey) {
				dom.fileUploadDirectoryInput?.click?.();
				return;
			}
			dom.fileUploadInput?.click?.();
        };
        dom.fileUploadDropzone.ondragenter = (event) => {
            event.preventDefault();
            if (modalState.fileUploadLocked) {
                return;
            }
            setFileUploadDropzoneActive(true);
        };
        dom.fileUploadDropzone.ondragover = (event) => {
            event.preventDefault();
            if (modalState.fileUploadLocked) {
                return;
            }
            setFileUploadDropzoneActive(true);
        };
        dom.fileUploadDropzone.ondragleave = (event) => {
            event.preventDefault();
            if (event.target === dom.fileUploadDropzone) {
                setFileUploadDropzoneActive(false);
            }
        };
        dom.fileUploadDropzone.ondrop = (event) => {
            event.preventDefault();
            if (modalState.fileUploadLocked) {
                return;
            }
            setFileUploadDropzoneActive(false);
			void (async () => {
				try {
					const items = await readDroppedUploadItems(event.dataTransfer);
					replaceFileUploadItemsWithItems(items);
				} catch (error) {
					console.error('[控制台页] 读取拖拽文件失败:', error);
					await showAlert(getUploadErrorText(error), { title: 'UPLOAD' });
				}
			})();
        };
    }
    if (dom.fileUploadInput) {
        dom.fileUploadInput.onchange = (event) => {
            if (modalState.fileUploadLocked) {
                return;
            }
            replaceFileUploadItems(event.target.files || []);
        };
    }
	if (dom.fileUploadDirectoryInput) {
		dom.fileUploadDirectoryInput.onchange = (event) => {
			if (modalState.fileUploadLocked) {
				return;
			}
			replaceFileUploadItems(event.target.files || []);
		};
	}
    if (dom.fileUploadList) {
        dom.fileUploadList.onclick = (event) => {
            const removeBtn = event.target.closest('.file-upload-remove');
            if (!removeBtn) {
                return;
            }
            removeFileUploadItem(removeBtn.dataset.id || '');
        };
    }
    if (dom.fileCreateModal) {
        dom.fileCreateModal.ondragover = (event) => {
            event.preventDefault();
        };
    }
};

export const bootFileCreateModal = (options = {}) => {
    modalState.onApplyFileList = options.onApplyFileList || null;
    modalState.onRequestReload = options.onRequestReload || null;
	modalState.getCurrentDir = options.getCurrentDir || null;
    bindEvents();
	return {
		open,
		close,
		openUploadWithDataTransfer,
	};
};
