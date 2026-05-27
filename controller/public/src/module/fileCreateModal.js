import { mainModalOverlay, state } from "../ui.js";
import { DEFAULT_UI_REFRESH_INTERVAL_MS, clearTimer, closeAnimatedModal, formatFileSize, getUploadErrorText, openAnimatedModal, setActionsDisabled, withActionsDisabled } from '../utils/utils.js';
import { abortFileUpload, createDirectory, createTextFileAdaptive, initFileUpload, uploadFileChunk, uploadFileSingle } from '../api/file.js';
import { showAlert, showConfirm } from './dialog.js';
import { applyFileUploadFolderGroupView, applyFileUploadItemView, buildFileUploadFolderGroupNode, buildFileUploadItemNode, ceilTo, computeFolderGroupAggregates, renderFileUploadSummaryText } from './fileUploadView.js';
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
						<span id="fileUploadSpeed" class="file-upload-speed">0 B/s</span>
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
	fileUploadSpeed: document.getElementById('fileUploadSpeed'),
	fileUploadSummary: document.getElementById('fileUploadSummary'),
	fileUploadList: document.getElementById('fileUploadList'),
	fileCreateActions: document.querySelector('#fileCreateForm .modal-actions'),
};

const FILE_CREATE_RECENT_LIMIT = 10;
const RECENT_FILE_NAMES_KEY = 'IpacPanel.recentCreatedFileNames';
const RECENT_DIR_NAMES_KEY = 'IpacPanel.recentCreatedDirNames';

const truncateInputValue = (input, maxLength) => {
	const value = InputValidation.truncateText(input.value || '', maxLength);
	input.value = value;
	return value;
};

const getUploadItemId = (itemOrId) => typeof itemOrId === 'string' ? itemOrId : String(itemOrId?.id || '');

const rememberUploadItem = (item) => {
	const id = getUploadItemId(item);
	if (!id || !item) {
		return;
	}
	modalState.fileUploadItemById.set(id, item);
	// 递归索引文件夹组的子项
	if (item.kind === 'folder-group' && item.children) {
		item.children.forEach(rememberUploadItem);
	}
};

const forgetUploadItem = (itemOrId) => {
	const id = getUploadItemId(itemOrId);
	if (!id) {
		return;
	}
	// 如果是文件夹组，递归清理子项
	const item = modalState.fileUploadItemById.get(id);
	if (item?.kind === 'folder-group' && item.children) {
		item.children.forEach((c) => forgetUploadItem(c.id));
	}
	modalState.fileUploadItemById.delete(id);
	modalState.fileUploadRowById.delete(id);
	modalState.fileUploadRemovedIds.delete(id);
	modalState.fileUploadAbortControllers.delete(id);
	modalState.fileUploadActiveContexts.delete(id);
};

/** 统计扁平（叶子）上传项数量 */
const countUploadLeafItems = (items) => {
	let count = 0;
	for (const item of items || []) {
		if (item.kind === 'folder-group') {
			count += countUploadLeafItems(item.children);
		} else {
			count += 1;
		}
	}
	return count;
};

/** 获取扁平（叶子）上传项数组 */
const getUploadLeafItems = () => {
	const result = [];
	for (const item of modalState.fileUploadItems) {
		if (item.kind === 'folder-group') {
			result.push(...item.children);
		} else {
			result.push(item);
		}
	}
	return result;
};

/**
 * 递归查找项（支持 folder-group 嵌套）
 */
const findUploadItemInTree = (items, id) => {
	for (const item of items || []) {
		if (item.id === id) return item;
		if (item.kind === 'folder-group') {
			const found = findUploadItemInTree(item.children, id);
			if (found) return found;
		}
	}
	return null;
};

/**
 * 从树中移除项，支持 folder-group 嵌套；
 * 移除 folder-group 时一并清除子项；清空子项后自动移除空组。
 */
const removeUploadItemFromTree = (items, id) => {
	if (!Array.isArray(items)) return null;
	for (let i = items.length - 1; i >= 0; i -= 1) {
		const item = items[i];
		if (item.id === id) {
			if (item.kind === 'folder-group') {
				(item.children || []).forEach((c) => forgetUploadItem(c.id));
			}
			items.splice(i, 1);
			forgetUploadItem(id);
			return item;
		}
		if (item.kind === 'folder-group') {
			const removed = removeUploadItemFromTree(item.children, id);
			if (removed) {
				// 重新计算父组聚合（不会更新 DOM，由外层统一渲染）
				updateFolderGroupAggregates(item);
				if (item.children.length === 0) {
					// 空组也移除
					items.splice(i, 1);
					forgetUploadItem(item.id);
				}
				return removed;
			}
		}
	}
	return null;
};

const removeUploadItemById = (itemOrId) => {
	const id = getUploadItemId(itemOrId);
	if (!id) {
		return;
	}
	removeUploadItemFromTree(modalState.fileUploadItems, id);
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
    fileUploadScanControllers: new Map(),
    fileUploadScanPromises: [],
    fileUploadScanPromiseByGroupId: new Map(),
    fileUploadScanDebounceTimer: null,
    fileUploadSpeedBytes: 0,
    fileUploadSpeedLastTick: 0,
    fileUploadSpeedValue: 0,
    fileUploadSpeedTimer: null,
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
    dom.fileCreateTypeFile.classList.toggle('active', type === 'file');
    dom.fileCreateTypeDir.classList.toggle('active', type === 'dir');
    dom.fileCreateTypeUpload.classList.toggle('active', type === 'upload');
    dom.fileCreatePageFile.classList.toggle('active', type === 'file');
    dom.fileCreatePageDir.classList.toggle('active', type === 'dir');
    dom.fileCreatePageUpload.classList.toggle('active', type === 'upload');
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
        // 使用缓存值保持分母/成功数在自动删除期间稳定
        const successCount = Math.max(0, modalState.fileUploadStats.success || 0);
        const leafItems = getUploadLeafItems();
        const failedCount = leafItems.filter((item) => item.status === 'FAILED').length;
        const total = Math.max(0, Number(modalState.fileUploadStats.total || 0)) || leafItems.length;
        const summaryText = renderFileUploadSummaryText({ success: successCount, total, failed: failedCount });
        dom.fileUploadSummary.innerText = summaryText;
        dom.fileUploadSummary.parentElement.classList.toggle('hidden', !summaryText);
    }
    renderFileUploadSpeed();
};

/** 重置上传速度跟踪状态 */
const resetFileUploadSpeed = () => {
    modalState.fileUploadSpeedBytes = 0;
    modalState.fileUploadSpeedLastTick = 0;
    modalState.fileUploadSpeedValue = 0;
};

/** 记录新上传的字节数 */
const recordFileUploadBytes = (delta) => {
    modalState.fileUploadSpeedBytes += delta;
};

/** 渲染当前速度到 DOM */
const renderFileUploadSpeed = () => {
    const el = dom.fileUploadSpeed;
    if (!el) return;
    const speed = modalState.fileUploadSpeedValue || 0;
    el.textContent = speed > 0 ? `${formatFileSize(speed)}/s` : '0 B/s';
};

/** 启动速度定时器（每秒计算并更新） */
const startFileUploadSpeedTimer = () => {
    if (modalState.fileUploadSpeedTimer) return;
    modalState.fileUploadSpeedLastTick = Date.now();
    modalState.fileUploadSpeedTimer = window.setInterval(() => {
        const now = Date.now();
        const elapsed = now - modalState.fileUploadSpeedLastTick;
        if (elapsed > 0) {
            modalState.fileUploadSpeedValue = modalState.fileUploadSpeedBytes / (elapsed / 1000);
        }
        modalState.fileUploadSpeedBytes = 0;
        modalState.fileUploadSpeedLastTick = now;
        renderFileUploadSpeed();
    }, 1000);
};

/** 停止速度定时器并显示 0 B/s */
const stopFileUploadSpeedTimer = () => {
    if (modalState.fileUploadSpeedTimer) {
        clearInterval(modalState.fileUploadSpeedTimer);
        modalState.fileUploadSpeedTimer = null;
    }
    modalState.fileUploadSpeedValue = 0;
    renderFileUploadSpeed();
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

	// Build DOM tree
	const fragment = document.createDocumentFragment();
	for (const item of modalState.fileUploadItems) {
		if (item.kind === 'folder-group') {
			fragment.appendChild(buildFileUploadFolderGroupNode(item));
		} else {
			fragment.appendChild(buildFileUploadItemNode(item));
		}
	}
	dom.fileUploadList.replaceChildren(fragment);

	// Refresh row map — 查询所有层级中的项和文件夹组
	modalState.fileUploadRowById.clear();
	const allItemRows = dom.fileUploadList.querySelectorAll('.file-upload-item, .file-upload-folder-group');
	for (let i = 0; i < allItemRows.length; i += 1) {
		const row = allItemRows[i];
		const id = String(row.dataset.id || '');
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
    dom.fileUploadDropzone.classList.toggle('dragover', !!modalState.fileUploadDropzoneActive);
    dom.fileUploadDropzone.classList.toggle('compact', modalState.fileUploadItems.length > 0);
    dom.fileUploadDropzone.classList.toggle('locked', !!modalState.fileUploadLocked);
    dom.fileUploadOverwrite.closest('.file-upload-overwrite').classList.toggle('locked', !!modalState.fileUploadLocked);
    if (dom.fileUploadDropzone) {
        dom.fileUploadDropzone.disabled = !!modalState.fileUploadLocked;
    }
    if (dom.fileUploadOverwrite) {
        dom.fileUploadOverwrite.disabled = !!modalState.fileUploadLocked;
    }
};

const updateFileUploadSubmitState = () => {
    if (!dom.fileCreateSubmit) {
        return;
    }
    dom.fileCreateSubmit.disabled = modalState.currentFileCreateType === 'upload' && !!modalState.fileUploadLocked;
};

const resetFileCreateActionsState = () => {
    setActionsDisabled(dom.fileCreateActions, false);
};

const setFileUploadLocked = (locked) => {
    modalState.fileUploadLocked = !!locked;
    updateFileUploadDropzoneState();
    updateFileUploadSubmitState();
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
        if ((row.dataset.id || '') === targetId) {
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
	if (!row.parentNode) {
		throw new Error('文件上传行父元素缺失');
	}
    row.parentNode.removeChild(row);
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
            // 同步更新父文件夹组聚合
            if (item.parentId) {
                const parent = modalState.fileUploadItemById.get(item.parentId);
                if (parent?.kind === 'folder-group') {
                    updateFolderGroupAggregates(parent);
                }
            }
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
    modalState.fileUploadStats.total = countUploadLeafItems(modalState.fileUploadItems);
    modalState.fileUploadStats.success = 0;
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const resetFileUploadState = () => {
	abortAllScans();
	stopFileUploadSpeedTimer();
    resetFileCreateActionsState();
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
    resetFileCreateActionsState();
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
	if (!abortRemote || ctx.remoteAbortRequested || ctx.completed || ctx.mode === 'single') {
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

/** 重新计算文件夹组的聚合数据并更新 DOM */
const updateFolderGroupAggregates = (group) => {
	if (!group || group.kind !== 'folder-group') return;
	const aggr = computeFolderGroupAggregates(group);
	group.loaded = aggr.totalLoaded;
	group.size = aggr.totalSize;
	group.progress = aggr.progress;
	// 派生子状态
	let status = 'WAITING';
	if (aggr.doneCount === aggr.totalCount) {
		status = 'DONE';
	} else if (aggr.failedCount > 0 && aggr.doneCount + aggr.failedCount === aggr.totalCount) {
		status = 'FAILED';
	} else if (aggr.doneCount > 0) {
		status = 'UPLOADING';
	}
	group.status = status;
	// 更新 DOM
	const row = modalState.fileUploadRowById.get(group.id);
	if (row) {
		applyFileUploadFolderGroupView(row, group);
	}
};

/** 如果子项有 parentId，更新对应的父文件夹组 */
const updateParentFolderGroup = (item) => {
	if (!item || !item.parentId) return;
	const parent = modalState.fileUploadItemById.get(item.parentId);
	if (parent?.kind === 'folder-group') {
		updateFolderGroupAggregates(parent);
	}
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
		updateParentFolderGroup(item);
		renderFileUploadSummary();
		modalState.fileUploadDoneClearTimers.set(id, window.setTimeout(() => {
			modalState.fileUploadDoneClearTimers.delete(id);
			const current = modalState.fileUploadItemById.get(id) || null;
			if (!current || current.status !== 'DONE') {
				return;
			}

			if (current.parentId) {
				// 文件夹组子项：只删除 DOM 行，保留数据保证聚合稳定
				const parent = modalState.fileUploadItemById.get(current.parentId);
				if (parent?.kind === 'folder-group') {
					const removed = removeFileUploadItemRow(id);
					if (!removed) {
						renderFileUploadList();
					}
					updateFolderGroupAggregates(parent);
					renderFileUploadSummary();
					updateFileUploadDropzoneState();
					pruneFileUploadListIfEmpty();
					return;
				}
			}

			// 普通文件：完整删除数据和 DOM
			removeUploadItemById(id);
			const removed = removeFileUploadItemRow(id);
			if (!removed) {
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
		updateParentFolderGroup(item);
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

/** 文件夹扫描每批处理多少文件后向 UI 报告 */
const FOLDER_SCAN_BATCH_SIZE = 50;

/** 中止文件夹扫描任务 */
const abortFolderScan = (groupId) => {
	const controller = modalState.fileUploadScanControllers.get(groupId);
	if (controller) {
		controller.abort();
		modalState.fileUploadScanControllers.delete(groupId);
	}
	// 从等待数组中移除该组的扫描 promise，避免 uploadSelectedFiles 等待已中止的扫描
	const promise = modalState.fileUploadScanPromiseByGroupId.get(groupId);
	if (promise) {
		const idx = modalState.fileUploadScanPromises.indexOf(promise);
		if (idx !== -1) modalState.fileUploadScanPromises.splice(idx, 1);
		modalState.fileUploadScanPromiseByGroupId.delete(groupId);
	}
};

/** 中止所有进行中的文件夹扫描 */
const abortAllScans = () => {
	const groupIds = Array.from(modalState.fileUploadScanControllers.keys());
	groupIds.forEach(abortFolderScan);
	modalState.fileUploadScanPromises = [];
	modalState.fileUploadScanPromiseByGroupId.clear();
	if (modalState.fileUploadScanDebounceTimer) {
		clearTimeout(modalState.fileUploadScanDebounceTimer);
		modalState.fileUploadScanDebounceTimer = null;
	}
};

/** 创建扫描中的文件夹组占位项 */
const createScanningFolderGroup = (name) => ({
	id: `scan-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
	kind: 'folder-group',
	name,
	path: '',
	size: 0,
	loaded: 0,
	progress: 0,
	status: 'WAITING',
	errorMessage: '',
	expanded: false,
	scanState: 'scanning',
	scannedCount: 0,
	scannedSize: 0,
	children: [],
});

/** 由扫描的文件条目创建单个上传子项 */
const createUploadItemFromScannedFile = (file, relativePath, groupId, fileIndex) => {
	const item = createPlainUploadItem(file, `${groupId}-${Date.now()}-${fileIndex}`);
	const parts = getUploadPathParts(normalizeUploadRelativePath(relativePath));
	item.path = parts.slice(0, -1).join('/');
	item.name = parts[parts.length - 1] || item.name;
	item.parentId = groupId;
	return item;
};

/** 处理文件夹扫描的一批结果：添加子项、更新计数、调度 UI 刷新 */
const handleFolderScanBatch = (groupItem, { files, scannedCount, scannedSize }) => {
	const children = files.map((f, i) =>
		createUploadItemFromScannedFile(f.file, f.relativePath, groupItem.id, groupItem.children.length + i)
	);
	children.forEach(rememberUploadItem);
	groupItem.children.push(...children);
	groupItem.scannedCount = scannedCount;
	groupItem.scannedSize = scannedSize;
	scheduleFolderScanDomUpdate();
};

/** 文件夹扫描完成：建立空目录、计算聚合、转回正常模式 */
const handleFolderScanComplete = (groupItem, dirsFound) => {
	if (groupItem.scanState !== 'scanning') return;
	groupItem.scanState = null;
	delete groupItem.scannedCount;
	delete groupItem.scannedSize;

	// 如果组已被移除，直接清理并返回
	const stillExists = findUploadItemInTree(modalState.fileUploadItems, groupItem.id);
	if (!stillExists) {
		modalState.fileUploadScanControllers.delete(groupItem.id);
		return;
	}

	// 创建扫描中发现的空目录项
	if (dirsFound && dirsFound.size > 0) {
		const filePaths = new Set(
			groupItem.children
				.filter((c) => c.kind !== 'empty-directory')
				.map((c) => normalizeUploadRelativePath([c.path, c.name].filter(Boolean).join('/')))
		);
		let dirIndex = 0;
		for (const dirPath of dirsFound) {
			const normalizedDir = normalizeUploadRelativePath(dirPath);
			const hasFileInDir = Array.from(filePaths).some(
				(fp) => fp === normalizedDir || fp.startsWith(normalizedDir + '/')
			);
			if (!hasFileInDir) {
				groupItem.children.push({
					id: `${groupItem.id}-dir-${dirIndex}-${Date.now()}`,
					kind: 'empty-directory',
					parentId: groupItem.id,
					name: normalizedDir,
					size: 0,
					loaded: 0,
					progress: 0,
					status: 'WAITING',
					errorMessage: '',
				});
				dirIndex += 1;
			}
		}
	}

	const aggr = computeFolderGroupAggregates(groupItem);
	groupItem.loaded = aggr.totalLoaded;
	groupItem.size = aggr.totalSize;
	groupItem.progress = aggr.progress;
	groupItem.status = aggr.doneCount === aggr.totalCount ? 'DONE' : 'WAITING';

	modalState.fileUploadScanControllers.delete(groupItem.id);

	// 重建索引并刷新完整列表
	modalState.fileUploadItemById.clear();
	modalState.fileUploadItems.forEach(rememberUploadItem);
	modalState.fileUploadStats.total = countUploadLeafItems(modalState.fileUploadItems);
	modalState.fileUploadStats.success = countSuccessLeafItems(modalState.fileUploadItems);
	renderFileUploadList();
	updateFileUploadDropzoneState();
};

/** 调度扫描中文件夹组的 DOM 更新（防抖，与 UI 刷新间隔一致） */
const scheduleFolderScanDomUpdate = () => {
	if (modalState.fileUploadScanDebounceTimer) return;
	modalState.fileUploadScanDebounceTimer = setTimeout(() => {
		modalState.fileUploadScanDebounceTimer = null;
		for (const [groupId] of modalState.fileUploadScanControllers) {
			const group = findUploadItemInTree(modalState.fileUploadItems, groupId);
			const row = modalState.fileUploadRowById.get(groupId);
			if (group && row) {
				applyFileUploadFolderGroupView(row, group);
			}
		}
		renderFileUploadSummary();
	}, DEFAULT_UI_REFRESH_INTERVAL_MS);
};

/** 使用 FileSystemEntry API 迭代遍历目录树，分批报告进度 */
const scanEntryTree = async (entry, groupItem) => {
	const controller = modalState.fileUploadScanControllers.get(groupItem.id);
	const queue = [{ entry, path: entry.name }];
	let batch = [];
	let scannedCount = 0;
	let scannedSize = 0;
	const dirsFound = new Set();

	while (queue.length > 0) {
		if (controller?.signal.aborted) break;

		const current = queue.shift();
		if (current.entry.isFile) {
			const file = await readEntryFile(current.entry);
			batch.push({ file, relativePath: current.path });
			scannedCount += 1;
			scannedSize += file.size;
		} else if (current.entry.isDirectory) {
			dirsFound.add(current.path);
			const children = await readAllDirectoryEntries(current.entry.createReader());
			for (const child of children) {
				queue.push({ entry: child, path: `${current.path}/${child.name}` });
			}
		}

		if (batch.length >= FOLDER_SCAN_BATCH_SIZE) {
			handleFolderScanBatch(groupItem, { files: batch, scannedCount, scannedSize });
			batch = [];
			await new Promise((r) => setTimeout(r, 0));
		}
	}

	if (batch.length > 0) {
		handleFolderScanBatch(groupItem, { files: batch, scannedCount, scannedSize });
	}

	handleFolderScanComplete(groupItem, dirsFound);
};

/** 使用 FileSystemHandle API 迭代遍历目录树，分批报告进度 */
const scanHandleTree = async (handle, groupItem) => {
	const controller = modalState.fileUploadScanControllers.get(groupItem.id);
	const queue = [{ handle, path: handle.name }];
	let batch = [];
	let scannedCount = 0;
	let scannedSize = 0;
	const dirsFound = new Set();

	while (queue.length > 0) {
		if (controller?.signal.aborted) break;

		const current = queue.shift();
		if (current.handle.kind === 'file') {
			const file = await current.handle.getFile();
			batch.push({ file, relativePath: current.path });
			scannedCount += 1;
			scannedSize += file.size;
		} else if (current.handle.kind === 'directory') {
			dirsFound.add(current.path);
			for await (const child of current.handle.values()) {
				queue.push({ handle: child, path: `${current.path}/${child.name}` });
			}
		}

		if (batch.length >= FOLDER_SCAN_BATCH_SIZE) {
			handleFolderScanBatch(groupItem, { files: batch, scannedCount, scannedSize });
			batch = [];
			await new Promise((r) => setTimeout(r, 0));
		}
	}

	if (batch.length > 0) {
		handleFolderScanBatch(groupItem, { files: batch, scannedCount, scannedSize });
	}

	handleFolderScanComplete(groupItem, dirsFound);
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

const createFolderGroupItem = (pickedFiles, pickedDirs, index) => {
	const groupId = `folder-${Date.now()}-${index}`;
	const sorted = Array.from(pickedFiles || [])
		.map((entry) => ({
			file: entry.file,
			path: normalizeUploadRelativePath(entry.relativePath || entry.file?.name || ''),
		}))
		.filter((entry) => entry.file && entry.path)
		.sort((a, b) => compareUploadPath(a.path, b.path));

	const children = sorted.map((entry, fileIndex) => {
		const item = createPlainUploadItem(entry.file, `${index}-${fileIndex}`);
		const parts = getUploadPathParts(entry.path);
		item.path = parts.slice(0, -1).join('/');
		item.name = parts[parts.length - 1] || item.name;
		item.parentId = groupId;
		return item;
	});

	const emptyDirs = Array.from(pickedDirs || [])
		.map((path) => normalizeUploadRelativePath(path))
		.filter(Boolean)
		.filter((path) => !sorted.some((entry) => entry.path === path || entry.path.startsWith(`${path}/`)));
	emptyDirs.forEach((path, dirIndex) => {
		children.push({
			id: `${Date.now()}-${index}-dir-${dirIndex}-${path}`,
			kind: 'empty-directory',
			parentId: groupId,
			name: path,
			size: 0,
			loaded: 0,
			progress: 0,
			status: 'WAITING',
			errorMessage: '',
		});
	});

	const firstPath = sorted[0]?.path || '';
	const groupName = getUploadRootName(firstPath, 'folder');
	const totalSize = children.reduce((sum, c) => sum + (c.size || 0), 0);

	return {
		id: groupId,
		kind: 'folder-group',
		name: groupName,
		path: '',
		size: totalSize,
		loaded: 0,
		progress: 0,
		status: 'WAITING',
		errorMessage: '',
		expanded: false,
		children,
	};
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
		return [createFolderGroupItem(group.files, group.dirs, index)];
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

	// 直接读取的文件（非目录项）
	const directFiles = [];
	// 需要后台扫描的目录项
	const scanJobs = [];

	for (const dropped of droppedItems) {
		if (shouldSkipDirectories && (dropped.entry?.isDirectory || dropped.isDirectoryPlaceholder)) {
			continue;
		}
		if (dropped.entry) {
			if (dropped.entry.isFile) {
				const file = await readEntryFile(dropped.entry);
				directFiles.push({ file, relativePath: dropped.entry.name });
			} else if (dropped.entry.isDirectory) {
				scanJobs.push({ type: 'entry', entry: dropped.entry });
			}
			continue;
		}
		try {
			const handle = await readDataTransferItemHandle(dropped.item);
			if (handle) {
				if (shouldSkipDirectories && handle.kind === 'directory') {
					continue;
				}
				if (handle.kind === 'file') {
					const file = await handle.getFile();
					directFiles.push({ file, relativePath: handle.name });
				} else {
					scanJobs.push({ type: 'handle', handle });
				}
				continue;
			}
		} catch (error) {
			console.warn('[控制台页] 读取拖拽文件系统句柄失败, 将回退到 DataTransferItem:', error);
		}
		if (dropped.file) {
			if (shouldSkipDirectories && dropped.isDirectoryPlaceholder) {
				continue;
			}
			directFiles.push({ file: dropped.file, relativePath: dropped.file.name, maybeDirectoryPlaceholder: dropped.isDirectoryPlaceholder });
		}
	}

	// 回退：用 DataTransfer.files 补充缺失的文件
	if (!scanJobs.length && fallbackFiles.length > directFiles.length) {
		const seen = new Set(directFiles.map((entry) => getUploadFileSignature(entry.file)));
		fallbackFiles.forEach((file) => {
			const signature = getUploadFileSignature(file);
			if (seen.has(signature)) {
				return;
			}
			seen.add(signature);
			directFiles.push({ file, relativePath: file.name });
		});
	}

	// 从直接文件创建立即显示的项
	const resultItems = buildUploadItemsFromPickedEntries(directFiles, []);

	// 为每个目录创建扫描占位并启动后台扫描
	for (const job of scanJobs) {
		const name = job.entry?.name || job.handle?.name || 'folder';
		const groupItem = createScanningFolderGroup(name);
		resultItems.push(groupItem);

		const controller = new AbortController();
		modalState.fileUploadScanControllers.set(groupItem.id, controller);

		const scanFn = job.type === 'entry'
			? () => scanEntryTree(job.entry, groupItem)
			: () => scanHandleTree(job.handle, groupItem);

		const scanPromise = scanFn().catch((error) => {
			if (error?.name === 'AbortError') return;
			console.error('[控制台页] 文件夹扫描失败:', error);
			if (groupItem.scanState === 'scanning') {
				handleFolderScanComplete(groupItem);
			}
		});
		modalState.fileUploadScanPromiseByGroupId.set(groupItem.id, scanPromise);
		modalState.fileUploadScanPromises.push(scanPromise);
		scanPromise.finally(() => {
			modalState.fileUploadScanPromiseByGroupId.delete(groupItem.id);
			const idx = modalState.fileUploadScanPromises.indexOf(scanPromise);
			if (idx !== -1) modalState.fileUploadScanPromises.splice(idx, 1);
		});
	}

	return resultItems;
};

const uploadFileChunkWithRetry = async (instanceName, uploadId, index, chunk, onProgress, options = {}) => {
    const maxAttempts = Math.max(1, modalState.fileUploadChunkRetryCount || 1);
    let lastError = null;

    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
        try {
			return await uploadFileChunk(instanceName, uploadId, index, chunk, onProgress, options);
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

    throw lastError || new Error(`分块 ${index} 上传失败`);
};

const initFileUploadContext = async (instanceName, currentPath, item, overwrite, options = {}) => {
	const runToken = Number(options.runToken) || modalState.fileUploadRunToken;
	const file = item.file;
	const uploadSize = file.size;
	const uploadPath = [currentPath, item.path].filter(Boolean).join('/');
	const useSingleUpload = uploadSize <= modalState.fileUploadChunkSize;
	const controller = new AbortController();
	const ctx = {
		item,
		file,
		instanceName,
		runToken,
		mode: useSingleUpload ? 'single' : 'chunked',
		uploadId: '',
		chunkSize: uploadSize > modalState.fileUploadChunkSize ? modalState.fileUploadChunkSize : Math.max(uploadSize, 1),
		chunkCount: useSingleUpload ? 0 : 1,
		chunkProgress: useSingleUpload ? [0] : null,
		totalLoaded: 0,
		nextChunkIndex: 0,
		refreshProgress: null,
		failed: false,
		removed: false,
		controller,
		controllerAborted: false,
		merging: false,
		completeResult: null,
		remoteAbortRequested: false,
		completed: false,
	};
	ctx.refreshProgress = (status) => {
		updateUploadItemProgress(item, ctx.totalLoaded || 0, status);
	};
	ctx.removed = isUploadItemCanceled(item, runToken);
	modalState.fileUploadAbortControllers.set(item.id, ctx.controller);
	modalState.fileUploadActiveContexts.set(item.id, ctx);

	if (useSingleUpload) {
		ctx.uploadFile = async () => {
			if (ctx.failed || ctx.removed) {
				return;
			}
			const result = await uploadFileSingle(instanceName, uploadPath, item.name, ctx.file, overwrite, (loaded) => {
				const safeLoaded = Math.max(0, Math.min(uploadSize, loaded || 0));
				const delta = safeLoaded - (ctx.totalLoaded || 0);
				ctx.totalLoaded = safeLoaded;
				recordFileUploadBytes(delta);
				ctx.refreshProgress('UPLOADING');
			}, { signal: ctx.controller.signal });
			if (!result || typeof result !== 'object' || result.completed !== true) {
				throw new Error('上传协议异常: 缺少完成响应');
			}
			ctx.completeResult = result;
			const delta = uploadSize - (ctx.totalLoaded || 0);
			if (delta !== 0) {
				ctx.totalLoaded = uploadSize;
				recordFileUploadBytes(delta);
			}
			ctx.nextChunkIndex = 1;
			ctx.refreshProgress('UPLOADING');
		};
		ctx.refreshProgress('UPLOADING');
		return ctx;
	}
	const chunkSize = uploadSize > modalState.fileUploadChunkSize ? modalState.fileUploadChunkSize : Math.max(uploadSize, 1);
	const chunkCount = Math.max(1, Math.ceil(file.size / chunkSize));
	const initResult = await initFileUpload(instanceName, {
		path: uploadPath,
		name: item.name,
		size: uploadSize,
		chunk_size: chunkSize,
		chunk_count: chunkCount,
		overwrite,
	});
	if (!initResult?.upload_id) {
		throw new Error('UPLOAD INIT FAILED');
	}

	ctx.uploadId = initResult.upload_id;
	ctx.chunkSize = chunkSize;
	ctx.chunkCount = chunkCount;
	ctx.chunkProgress = new Array(chunkCount).fill(0);

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
			throw new Error(`分块 ${index} 源缺失`);
		}
		const start = localIndex * ctx.chunkSize;
		const end = Math.min(chunkFile.size, start + ctx.chunkSize);
		const chunk = chunkFile.slice(start, end);
		const result = await uploadFileChunkWithRetry(instanceName, ctx.uploadId, index, chunk, (loaded) => {
			const safeLoaded = Math.max(0, Math.min(chunk.size, loaded || 0));
			const prevLoaded = ctx.chunkProgress[index] || 0;
			const delta = safeLoaded - prevLoaded;
			ctx.chunkProgress[index] = safeLoaded;
			ctx.totalLoaded = Math.max(0, (ctx.totalLoaded || 0) + delta);
			recordFileUploadBytes(delta);
			ctx.refreshProgress('UPLOADING');
		}, { signal: ctx.controller.signal });
		if (result && typeof result === 'object' && result.completed === true) {
			ctx.completeResult = result;
		}
		const prevLoaded = ctx.chunkProgress[index] || 0;
		if (prevLoaded !== chunk.size) {
			const chunkDelta = chunk.size - prevLoaded;
			ctx.chunkProgress[index] = chunk.size;
			ctx.totalLoaded = Math.max(0, (ctx.totalLoaded || 0) + chunkDelta);
			recordFileUploadBytes(chunkDelta);
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
		if (ctx.mode === 'single') {
			return ctx.nextChunkIndex < 1;
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
		const totalParts = ctx.mode === 'single' ? 1 : ctx.chunkCount;
		if (ctx.nextChunkIndex < totalParts) {
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
		const p = ctx.mode === 'single' ? ctx.uploadFile() : ctx.uploadChunk(idx);
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
			const totalParts = ctx.mode === 'single' ? 1 : ctx.chunkCount;
			if (pending > 0 || ctx.nextChunkIndex < totalParts) {
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
					await cleanupFileUploadContext(ctx, {
						abortController: true,
						abortRemote: true,
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

const hasFailedFileUploads = () => getUploadLeafItems().some((item) => item.status === 'FAILED');

const setFileUploadAwaitConfirm = (value) => {
    modalState.fileUploadAwaitConfirm = !!value;
    renderFileCreatePage();
};

/** 递归重置文件夹组内失败子项 */
const resetFolderGroupChildren = (group) => {
	if (!group || group.kind !== 'folder-group') return;
	group.children = group.children
		.filter((child) => child.status !== 'DONE')
		.map((child) => {
			if (child.kind === 'folder-group') {
				resetFolderGroupChildren(child);
				return child;
			}
			if (child.status !== 'FAILED') return child;
			return { ...child, loaded: 0, progress: 0, status: 'WAITING', errorMessage: '' };
		});
	const aggr = computeFolderGroupAggregates(group);
	group.loaded = aggr.totalLoaded;
	group.size = aggr.totalSize;
	group.progress = aggr.progress;
	group.status = aggr.doneCount === aggr.totalCount ? 'DONE' : 'WAITING';
};

const prepareRetryFileUploads = () => {
    modalState.fileUploadRemovedIds.clear();
    modalState.fileUploadItems = modalState.fileUploadItems
        .filter((item) => {
			if (item.kind === 'folder-group') {
				resetFolderGroupChildren(item);
				return item.children.length > 0;
			}
			return item.status !== 'DONE';
		})
        .map((item) => {
			if (item.kind === 'folder-group') return item;
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
	modalState.fileUploadItems.forEach(rememberUploadItem);
    modalState.fileUploadStats.total = countUploadLeafItems(modalState.fileUploadItems);
    modalState.fileUploadStats.success = 0;
    modalState.fileUploadAwaitConfirm = false;
    renderFileCreatePage();
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const uploadSelectedFiles = async (overwrite) => {
	const runToken = modalState.fileUploadRunToken;
	let hasSuccess = false;
	let hasFinished = false;
	let hasDone = false;
	setFileUploadLocked(true);
	resetFileUploadSpeed();
	startFileUploadSpeedTimer();
	try {
		// 等待所有进行中的文件夹扫描完成；上传提交从扫描等待阶段开始锁定。
		if (modalState.fileUploadScanPromises.length > 0) {
			await Promise.all(modalState.fileUploadScanPromises);
			if (runToken !== modalState.fileUploadRunToken) {
				return false;
			}
			modalState.fileUploadScanPromises = [];
		}

		const instanceName = state.currentInstanceName;
		const currentPath = getCurrentDir();
		const items = getUploadLeafItems();
		if (!instanceName || !items.length) {
			await showAlert('请选择至少一个文件', { title: 'INPUT' });
			return false;
		}

		const limit = Math.max(1, modalState.fileUploadConcurrency || 1);
		items.forEach((item) => {
			updateUploadItemProgress(item, 0, 'WAITING');
		});

		let nextIndex = 0;
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
						// 空目录没有文件触发 upload/init 自动创建, 直接通过 API 逐级创建
						const dirParts = normalizeUploadRelativePath(item.name).split('/').filter(Boolean);
						let dirAccumPath = String(currentPath || '').replace(/^\/+|\/+$/g, '');
						for (const part of dirParts) {
							const result = await createDirectory(instanceName, dirAccumPath, part);
							if (!result.ok) {
								throw new Error(result.error || '创建上传目录失败');
							}
							dirAccumPath = dirAccumPath ? `${dirAccumPath}/${part}` : part;
						}
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
					if (!ctx.completeResult || ctx.completeResult.completed !== true) {
						throw new Error('上传协议异常: 缺少完成响应');
					}
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
		if (runToken === modalState.fileUploadRunToken) {
			stopFileUploadSpeedTimer();
			setFileUploadLocked(false);
			cleanupDoneFolderGroupChildren();
			trimFinishedUploadStats();
		}
	}
    if (runToken !== modalState.fileUploadRunToken) {
        return false;
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

/** 获取上传项的去重标识 */
const getUploadItemDedupKey = (item) => {
	if (item.kind === 'folder-group') {
		return `group:${item.name}`;
	}
	return `file:${item.path || ''}:${item.name}:${item.size || 0}`;
};

/** 统计 DONE 状态的叶子项数 */
const countSuccessLeafItems = (items) => {
	let count = 0;
	for (const item of items || []) {
		if (item.kind === 'folder-group') {
			count += countSuccessLeafItems(item.children);
		} else if (item.status === 'DONE') {
			count += 1;
		}
	}
	return count;
};

/** 追加文件到已有上传列表（去重替换） */
const appendFileUploadItemsWithItems = (items) => {
    setFileUploadAwaitConfirm(false);

	// 构建已有项的去重映射
	const dedupMap = new Map();
	const buildDedupMap = (itemList) => {
		for (const item of itemList) {
			dedupMap.set(getUploadItemDedupKey(item), item);
			if (item.kind === 'folder-group' && item.children) {
				buildDedupMap(item.children);
			}
		}
	};
	buildDedupMap(modalState.fileUploadItems);

	for (const newItem of items) {
		const key = getUploadItemDedupKey(newItem);
		const existing = dedupMap.get(key);

		if (newItem.kind === 'folder-group' && existing?.kind === 'folder-group') {
			// 同名文件夹组：合并子项；如果新组正在扫描则中止
			if (newItem.scanState === 'scanning') {
				abortFolderScan(newItem.id);
			}
			const childDedup = new Map();
			existing.children.forEach((c) => childDedup.set(getUploadItemDedupKey(c), c));
			for (const newChild of newItem.children) {
				const childKey = getUploadItemDedupKey(newChild);
				const oldChild = childDedup.get(childKey);
				if (oldChild) {
					cancelFileUploadItem(oldChild.id);
					clearFileUploadDoneClearTimer(oldChild.id);
					const idx = existing.children.indexOf(oldChild);
					if (idx !== -1) {
						existing.children[idx] = newChild;
					}
				} else {
					existing.children.push(newChild);
				}
			}
			// 合并后立即重新计算聚合，保持数据一致性
			updateFolderGroupAggregates(existing);
			dedupMap.set(key, existing);
			continue;
		}

		if (existing) {
			// 替换已有项
			cancelFileUploadItem(existing.id);
			clearFileUploadDoneClearTimer(existing.id);
			removeUploadItemFromTree(modalState.fileUploadItems, existing.id);
			modalState.fileUploadItems.push(newItem);
			dedupMap.set(key, newItem);
		} else {
			// 追加新项
			modalState.fileUploadItems.push(newItem);
			dedupMap.set(key, newItem);
		}
	}

	// 重建索引和统计
	modalState.fileUploadItemById.clear();
	modalState.fileUploadItems.forEach(rememberUploadItem);
	modalState.fileUploadStats.total = countUploadLeafItems(modalState.fileUploadItems);
	modalState.fileUploadStats.success = countSuccessLeafItems(modalState.fileUploadItems);

	renderFileUploadList();
	updateFileUploadDropzoneState();

	if (dom.fileUploadInput) {
		dom.fileUploadInput.value = '';
	}
	if (dom.fileUploadDirectoryInput) {
		dom.fileUploadDirectoryInput.value = '';
	}
};

const appendFileUploadItems = (files) => {
	const items = buildUploadItemsFromPickedEntries(Array.from(files || []).map((file) => ({
		file,
		relativePath: file.webkitRelativePath || file.name,
		isDirectory: !!file.webkitRelativePath,
	})));
	appendFileUploadItemsWithItems(items);
};

/** 取消文件夹组内所有子项上传 */
const cancelFolderGroupChildren = (group) => {
	if (!group || group.kind !== 'folder-group') return;
	(group.children || []).forEach((child) => {
		cancelFileUploadItem(child.id);
		clearFileUploadDoneClearTimer(child.id);
	});
};

const removeFileUploadItem = (id) => {
    const item = findUploadItemInTree(modalState.fileUploadItems, id);
    if (item?.kind === 'folder-group') {
		abortFolderScan(item.id);
		cancelFolderGroupChildren(item);
    }
    // 不 await，保证 UI 立即响应，同时后台会异步清理/取消
    cancelFileUploadItem(id);
	clearFileUploadDoneClearTimer(id);
	removeUploadItemById(id);
	// 手动删除后重算 PROGRESS 缓存值（文件夹组子项已在 removeUploadItemById 中更新聚合）
	modalState.fileUploadStats.total = countUploadLeafItems(modalState.fileUploadItems);
	modalState.fileUploadStats.success = countSuccessLeafItems(modalState.fileUploadItems);
    renderFileUploadList();
    updateFileUploadDropzoneState();
};

const trimFinishedUploadStats = () => {
	renderFileUploadSummary();
};

/** 上传完成后清理文件夹组中已被 DOM 删除的 DONE 子项，空文件夹组一并移除 */
const cleanupDoneFolderGroupChildren = () => {
	let changed = false;
	for (let i = modalState.fileUploadItems.length - 1; i >= 0; i--) {
		const item = modalState.fileUploadItems[i];
		if (item.kind !== 'folder-group') continue;

		const doneChildren = item.children.filter((c) => c.status === 'DONE');
		if (doneChildren.length > 0) {
			doneChildren.forEach((c) => {
				clearFileUploadDoneClearTimer(c.id);
				forgetUploadItem(c.id);
			});
			item.children = item.children.filter((c) => c.status !== 'DONE');
			changed = true;
		}

		if (item.children.length === 0) {
			modalState.fileUploadItems.splice(i, 1);
			forgetUploadItem(item.id);
			changed = true;
		} else {
			updateFolderGroupAggregates(item);
		}
	}
	if (changed) {
		modalState.fileUploadStats.total = countUploadLeafItems(modalState.fileUploadItems);
		modalState.fileUploadStats.success = countSuccessLeafItems(modalState.fileUploadItems);
		renderFileUploadList();
		renderFileUploadSummary();
		updateFileUploadDropzoneState();
		pruneFileUploadListIfEmpty();
	}
};

const setFileCreateType = (type) => {
    if (modalState.fileUploadLocked) {
        return;
    }
    if (type === 'dir' || type === 'upload') {
        modalState.currentFileCreateType = type;
    } else {
        modalState.currentFileCreateType = 'file';
    }
    renderFileCreatePage();
    updateFileUploadSubmitState();
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
			? !!dom.fileUploadOverwrite.checked
			: !!dom.fileCreateOverwrite.checked,
    };
};

const resetFileCreateUploadForm = () => {
    resetFileUploadState();
    modalState.currentFileCreateType = 'upload';
    renderFileCreatePage();
};

const handleFileCreateSubmit = async (event) => {
    event.preventDefault();

	const { type, name, content, overwrite } = getFileCreateFormValue();
	if (type === 'upload') {
		if (modalState.fileUploadLocked) {
			return;
		}
		if (modalState.fileUploadAwaitConfirm) {
			resetFileCreateUploadForm();
			return;
		}
		if (hasFailedFileUploads()) {
			prepareRetryFileUploads();
		}
		const runToken = modalState.fileUploadRunToken;
		const ok = await uploadSelectedFiles(overwrite);
		if (ok && runToken === modalState.fileUploadRunToken) {
			setFileUploadAwaitConfirm(!hasFailedFileUploads());
		}
		return;
	}

	await withActionsDisabled(dom.fileCreateActions, async () => {
		if (!name) {
			await showAlert('名称不能为空', { title: 'INPUT' });
			if (type === 'dir') {
				dom.fileCreateDirName.focus();
			} else {
				dom.fileCreateName.focus();
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

const collectAllUploadIds = () => {
	const ids = [];
	const walk = (items) => {
		for (const item of items || []) {
			ids.push(item.id);
			if (item.kind === 'folder-group') {
				walk(item.children);
			}
		}
	};
	walk(modalState.fileUploadItems);
	return ids;
};

const cancelAllFileUploads = () => {
	abortAllScans();
    const ids = collectAllUploadIds();
    ids.forEach((id) => {
        // 不 await，避免阻塞关闭动画
        cancelFileUploadItem(id);
    });
};

const tryCloseWithConfirm = async () => {
    if (hasActiveFileUploads()) {
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
	resetFileCreateActionsState();
	dom.fileCreateForm.reset();
    if (dom.fileCreateContent) {
        dom.fileCreateContent.value = '';
    }
	if (dom.fileCreateOverwrite) {
		dom.fileCreateOverwrite.checked = false;
	}
	const initialType = String(options?.type || '').trim() === 'upload' ? 'upload' : 'file';
	modalState.currentFileCreateType = initialType;
    resetFileUploadState();
	resetFileCreateActionsState();
	modalState.currentFileCreateType = initialType;
	renderFileCreateTargetDir();
	renderRecentCreatedNameLists();
	renderFileCreatePage();
	openAnimatedModal(dom.fileCreateModal);
	if (initialType === 'upload') {
		dom.fileUploadDropzone.focus();
	} else {
		dom.fileCreateName.focus();
	}
};

const openUploadWithDataTransfer = async (dataTransfer) => {
	open({ type: 'upload' });
	try {
		const items = await readDroppedUploadItems(dataTransfer);
		appendFileUploadItemsWithItems(items);
	} catch (error) {
		console.error('[控制台页] 读取拖拽文件失败:', error);
		await showAlert(getUploadErrorText(error), { title: 'UPLOAD' });
	}
};

const close = () => {
    if (!dom.fileCreateModal) return;
    resetFileUploadState();
	resetFileCreateActionsState();
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
				dom.fileUploadDirectoryInput.click();
				return;
			}
			dom.fileUploadInput.click();
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
					appendFileUploadItemsWithItems(items);
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
            appendFileUploadItems(event.target.files || []);
        };
    }
	if (dom.fileUploadDirectoryInput) {
		dom.fileUploadDirectoryInput.onchange = (event) => {
			if (modalState.fileUploadLocked) {
				return;
			}
			appendFileUploadItems(event.target.files || []);
		};
	}
    if (dom.fileUploadList) {
        dom.fileUploadList.onclick = (event) => {
            const removeBtn = event.target.closest('.file-upload-remove');
            if (removeBtn) {
                removeFileUploadItem(removeBtn.dataset.id || '');
                return;
            }
            // 文件夹组折叠/展开
            const toggleBtn = event.target.closest('.file-upload-folder-header');
            if (toggleBtn) {
                const folderId = toggleBtn.dataset.folderId
                    || toggleBtn.closest('.file-upload-folder-group')?.dataset?.id
                    || '';
                if (!folderId) return;
                const group = modalState.fileUploadItemById.get(folderId);
                if (!group || group.kind !== 'folder-group') return;
                group.expanded = !group.expanded;
                // 局部切换子项容器而非全量重渲染
                const container = dom.fileUploadList.querySelector(`.file-upload-folder-group[data-id="${folderId}"]`);
                if (container) {
                    container.classList.toggle('expanded', group.expanded);
                    const childrenContainer = container.querySelector('.file-upload-folder-children');
                    if (childrenContainer) {
                        childrenContainer.classList.toggle('hidden', !group.expanded);
                    }
                }
            }
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
