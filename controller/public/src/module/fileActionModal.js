import { mainModalOverlay, state } from "../ui.js";
import { buildAuthedFileRawUrl } from '../api/core.js';
import { clearTimer, formatFileSize, withActionsDisabled } from '../utils/utils.js';
import { deleteFile, downloadFileArchive, renameFile, streamFileExtractAction, triggerSilentDownload } from '../api/file.js';
import { showAlert, showConfirm } from './dialog.js';
import { getFileType } from '../utils/icon.js';
import { InputValidation } from '../utils/inputValidation.js';
import { readJsonSSEStream } from '../utils/sse.js';
import { applyTabPageState, bindTabPageButtons } from '../utils/modalHelper.js';

console.log('[模块] FileActionModal 加载中...');

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="fileActionModal" class="modal-overlay">
        <div class="modal-card file-action-modal-card">
			<div class="modal-header">
				<span id="fileActionTitle" class="modal-title">FILE ACTION</span>
				<button id="fileActionClose" class="modal-close" type="button">×</button>
			</div>
            <div class="modal-form file-action-form">
				<div id="fileActionTabs" class="filter-group file-action-tabs hidden">
					<button id="fileActionTabInfo" class="filter-btn active" type="button" data-page="info">INFO</button>
					<button id="fileActionTabExtract" class="filter-btn" type="button" data-page="extract">EXTRACT</button>
					<button id="fileActionTabDelete" class="filter-btn" type="button" data-page="delete">DELETE</button>
				</div>
				<div class="file-action-body">
					<form id="fileActionPageInfo" class="modal-page file-action-page active">
						<div class="field-group">
							<span>NAME</span>
							<input id="fileRenameName" type="text" autocomplete="off" maxlength="${InputValidation.limits.fileName}" required>
						</div>
						<div class="field-group">
							<span>PATH</span>
							<div id="fileActionInfoParentDir" class="file-action-static"></div>
						</div>
						<div class="field-group">
							<span>SIZE</span>
							<div id="fileActionInfoSize" class="file-action-static"></div>
						</div>
						<div class="field-group">
							<span>MODIFIED</span>
							<div id="fileActionInfoModified" class="file-action-static"></div>
						</div>
						<div class="modal-actions modal-actions-split">
							<div class="modal-actions-group">
								<button class="btn" type="button" id="fileActionDownload">DOWNLOAD</button>
							</div>
							<div class="modal-actions-group">
								<button class="btn" type="button" id="fileActionCancel">CANCEL</button>
								<button class="btn btn-start" type="submit">SAVE</button>
							</div>
						</div>
					</form>
					<form id="fileActionPageExtract" class="modal-page file-action-page">
						<div class="field-group">
							<span>ARCHIVE</span>
							<div id="fileExtractSourcePath" class="file-action-static"></div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span>TARGET</span>
							<div class="file-extract-target-options">
								<label class="checkbox-group file-extract-choice-row">
									<input id="fileExtractModeCustom" name="fileExtractMode" type="radio" value="custom" checked>
									<span>解压到指定目录</span>
								</label>
								<label class="checkbox-group file-extract-choice-row">
									<input id="fileExtractModeCurrent" name="fileExtractMode" type="radio" value="current">
									<span>解压到当前位置</span>
								</label>
							</div>
						</div>
						<div id="fileExtractDirGroup" class="field-group hidden">
							<span>DIR NAME</span>
							<input id="fileExtractDirName" type="text" autocomplete="off" spellcheck="false" maxlength="${InputValidation.limits.fileName}" required>
						</div>
						<label id="fileExtractOverwriteGroup" class="checkbox-group">
							<input id="fileExtractOverwrite" type="checkbox">
							<span>覆盖已存在文件</span>
						</label>
						<div id="fileExtractProgress" class="file-extract-progress hidden">
							<div class="file-upload-item-meta file-extract-progress-meta">
								<span id="fileExtractProgressText">准备解压...</span>
								<span id="fileExtractProgressPercent">0.00%</span>
							</div>
							<div class="file-upload-progress"><span id="fileExtractProgressBar" class="progress-fill-zero"></span></div>
							<div id="fileExtractProgressDetail" class="file-batch-progress-text"></div>
						</div>
						<div id="fileExtractActions" class="modal-actions">
							<button class="btn" type="button" id="fileExtractCancel">CANCEL</button>
							<button class="btn btn-start" type="submit" id="fileExtractSubmit">RUN</button>
						</div>
					</form>
					<form id="fileActionPageDelete" class="modal-page file-action-page">
						<div class="field-group">
							<span>WARNING</span>
							<div class="file-action-static file-delete-warning">此操作不可撤销, 请确认删除当前对象</div>
						</div>
						<div class="field-group">
							<span>TARGET</span>
							<div id="fileDeleteTarget" class="file-action-static"></div>
						</div>
						<div id="fileDeleteActions" class="modal-actions">
							<button class="btn" type="button" id="fileDeleteCancel">CANCEL</button>
							<button class="btn" type="submit" id="fileDeleteSubmit">DELETE</button>
						</div>
					</form>
                </div>
            </div>
        </div>
    </div>
`);

const dom = {
	fileActionModal: document.getElementById('fileActionModal'),
	fileActionClose: document.getElementById('fileActionClose'),
	fileActionCancel: document.getElementById('fileActionCancel'),
	fileActionTitle: document.getElementById('fileActionTitle'),
	fileActionPageInfo: document.getElementById('fileActionPageInfo'),
	fileActionInfoParentDir: document.getElementById('fileActionInfoParentDir'),
	fileActionInfoSize: document.getElementById('fileActionInfoSize'),
	fileActionInfoModified: document.getElementById('fileActionInfoModified'),
	fileRenameName: document.getElementById('fileRenameName'),
	fileActionDownload: document.getElementById('fileActionDownload'),
	fileInfoActions: document.querySelector('#fileActionPageInfo .modal-actions'),
	fileActionTabs: document.getElementById('fileActionTabs'),
	fileActionTabInfo: document.getElementById('fileActionTabInfo'),
	fileActionTabExtract: document.getElementById('fileActionTabExtract'),
	fileActionTabDelete: document.getElementById('fileActionTabDelete'),
	fileActionPageExtract: document.getElementById('fileActionPageExtract'),
	fileActionPageDelete: document.getElementById('fileActionPageDelete'),
	fileExtractSourcePath: document.getElementById('fileExtractSourcePath'),
	fileExtractModeCurrent: document.getElementById('fileExtractModeCurrent'),
	fileExtractModeCustom: document.getElementById('fileExtractModeCustom'),
	fileExtractDirGroup: document.getElementById('fileExtractDirGroup'),
	fileExtractOverwrite: document.getElementById('fileExtractOverwrite'),
	fileExtractDirName: document.getElementById('fileExtractDirName'),
	fileExtractProgress: document.getElementById('fileExtractProgress'),
	fileExtractProgressText: document.getElementById('fileExtractProgressText'),
	fileExtractProgressPercent: document.getElementById('fileExtractProgressPercent'),
	fileExtractProgressBar: document.getElementById('fileExtractProgressBar'),
	fileExtractProgressDetail: document.getElementById('fileExtractProgressDetail'),
	fileExtractActions: document.getElementById('fileExtractActions'),
	fileExtractCancel: document.getElementById('fileExtractCancel'),
	fileExtractSubmit: document.getElementById('fileExtractSubmit'),
	fileDeleteTarget: document.getElementById('fileDeleteTarget'),
	fileDeleteActions: document.getElementById('fileDeleteActions'),
	fileDeleteCancel: document.getElementById('fileDeleteCancel'),
	fileDeleteSubmit: document.getElementById('fileDeleteSubmit'),
};

const modalState = {
    fileActionModalCloseTimer: null,
    currentFileActionEntry: null,
    currentFileActionPage: 'info',
	initialPage: 'info',
    onApplyFileList: null,
	onRequestReload: null,
	getCurrentDir: null,
	isArchive: false,
	extracting: false,
	extractAbortController: null,
	extractRunId: 0,
	extractCanceling: false,
	extractProgress: {
		percent: 0,
		text: '',
		detail: '',
		error: '',
	},
	deleting: false,
	downloading: false,
    isBound: false,
};

const fileActionTabPages = [
	{ name: 'info', tab: dom.fileActionTabInfo, page: dom.fileActionPageInfo },
	{ name: 'extract', tab: dom.fileActionTabExtract, page: dom.fileActionPageExtract, enabled: () => modalState.isArchive },
	{ name: 'delete', tab: dom.fileActionTabDelete, page: dom.fileActionPageDelete },
];

const getCurrentDir = () => {
	if (typeof modalState.getCurrentDir === 'function') {
		return String(modalState.getCurrentDir() || '');
	}
	return '';
};

const applyFileActionPage = (page) => {
    modalState.currentFileActionPage = page;
	applyTabPageState(page, fileActionTabPages);
};

const resolveOpenPage = (requestedPage) => {
	const page = String(requestedPage || '').trim().toLowerCase();
	if (page === 'extract' && modalState.isArchive) {
		return 'extract';
	}
	if (page === 'delete') {
		return 'delete';
	}
	return 'info';
};

const focusRenameInput = () => {
	if (!dom.fileRenameName) {
		return;
	}
	dom.fileRenameName.focus();
};

const getEntryParentDirPath = (entry) => {
	if (!entry?.path) {
		return getCurrentDir();
	}
	let p = String(entry.path || '');
	if (!p) {
		return getCurrentDir();
	}
	if (p.endsWith('/') || p.endsWith('\\')) {
		p = p.slice(0, -1);
	}
	const lastSlash = p.lastIndexOf('/');
	const lastBackslash = p.lastIndexOf('\\');
	const idx = Math.max(lastSlash, lastBackslash);
	if (idx < 0) {
		return '';
	}
	return p.slice(0, idx + 1);
};

const stripArchiveExtension = (name) => {
	const value = String(name || '').trim();
	if (!value) {
		return '';
	}
	const lower = value.toLowerCase();
	const suffixes = [
		'.tar.gz', '.tar.bz2', '.tar.xz', '.tar.lzma', '.tar.lz4', '.tar.zst', '.tar.zstd', '.tar.tgz',
		'.zip', '.rar', '.7z', '.tar', '.gz', '.tgz', '.bz2', '.xz', '.lzma', '.lz4', '.zst', '.zstd', '.iso', '.cab', '.pkg',
	];
	for (const suffix of suffixes) {
		if (lower.endsWith(suffix) && value.length > suffix.length) {
			return value.slice(0, value.length - suffix.length);
		}
	}
	const dot = value.lastIndexOf('.');
	if (dot > 0) {
		return value.slice(0, dot);
	}
	return value;
};

const getExtractTargetPath = () => {
	const entry = modalState.currentFileActionEntry;
	const parentDir = getEntryParentDirPath(entry);
	if (!dom.fileExtractModeCustom.checked) {
		return parentDir || getCurrentDir();
	}
	const dirName = InputValidation.truncateText(dom.fileExtractDirName.value || '', InputValidation.limits.fileName).trim();
	if (!dirName) {
		return parentDir || getCurrentDir();
	}
	if (!parentDir) {
		return dirName;
	}
	const sep = parentDir.endsWith('/') || parentDir.endsWith('\\') ? '' : '/';
	return `${parentDir}${sep}${dirName}`;
};

const truncateTextInputValue = (input, maxLength) => {
	const value = InputValidation.truncateText(input.value || '', maxLength);
	if (input) {
		input.value = value;
	}
	return value;
};

const setExtractProgress = ({ percent, text, detail, error } = {}) => {
	if (typeof percent === 'number' && Number.isFinite(percent)) {
		modalState.extractProgress.percent = Math.max(0, Math.min(100, percent));
	}
	if (typeof text === 'string') {
		modalState.extractProgress.text = text;
	}
	if (typeof detail === 'string') {
		modalState.extractProgress.detail = detail;
	}
	if (typeof error === 'string') {
		modalState.extractProgress.error = error;
	}
	const current = modalState.extractProgress;
	if (dom.fileExtractProgressText) {
		dom.fileExtractProgressText.textContent = current.text || '准备解压...';
	}
	if (dom.fileExtractProgressPercent) {
		dom.fileExtractProgressPercent.textContent = `${current.percent.toFixed(2)}%`;
	}
	if (dom.fileExtractProgressBar) {
		dom.fileExtractProgressBar.style.width = `${current.percent.toFixed(5)}%`;
	}
	if (dom.fileExtractProgressDetail) {
		dom.fileExtractProgressDetail.textContent = current.error || current.detail || '';
		dom.fileExtractProgressDetail.classList.toggle('file-upload-error', !!current.error);
	}
	if (dom.fileExtractProgress) {
		const visible = modalState.extracting || !!current.detail || !!current.error || current.percent > 0;
		dom.fileExtractProgress.classList.toggle('hidden', !visible);
	}
	if (dom.fileExtractSubmit) {
		dom.fileExtractSubmit.disabled = modalState.extracting;
		dom.fileExtractSubmit.textContent = modalState.extracting ? 'RUNNING...' : 'RUN';
	}
	if (dom.fileExtractCancel) {
		dom.fileExtractCancel.disabled = modalState.extractCanceling;
		dom.fileExtractCancel.textContent = modalState.extractCanceling ? 'CANCELING...' : 'CANCEL';
	}
	for (const tab of [dom.fileActionTabInfo, dom.fileActionTabExtract, dom.fileActionTabDelete]) {
		if (tab) {
			tab.disabled = modalState.extracting;
		}
	}
	updateExtractTargetMode();
};

const resetExtractState = () => {
	modalState.extractRunId += 1;
	if (modalState.extractAbortController) {
		modalState.extractAbortController.abort();
	}
	modalState.extracting = false;
	modalState.extractAbortController = null;
	modalState.extractCanceling = false;
	modalState.extractProgress = {
		percent: 0,
		text: '准备解压...',
		detail: '',
		error: '',
	};
	setExtractProgress();
};

const isCurrentExtractTask = (abortController, runId) => {
	return modalState.extractAbortController === abortController && modalState.extractRunId === runId;
};

const updateExtractTargetMode = () => {
	if (dom.fileExtractDirGroup) {
		dom.fileExtractDirGroup.classList.toggle('hidden', !dom.fileExtractModeCustom.checked);
	}
	if (dom.fileExtractDirName) {
		dom.fileExtractDirName.disabled = !dom.fileExtractModeCustom.checked;
		dom.fileExtractDirName.required = !!dom.fileExtractModeCustom.checked;
	}
};

const extractErrorMessage = (error) => {
	const rawMessage = error instanceof Error
		? String(error.message || '').trim()
		: String(error || '').trim();
	if (!rawMessage) {
		return '解压失败';
	}
	try {
		const payload = JSON.parse(rawMessage);
		if (payload && typeof payload === 'object') {
			const message = String(payload.message || payload.error || payload.reason || '').trim();
			if (message) {
				return message;
			}
		}
	} catch (_) {
		// 非 JSON 错误消息直接展示原文。
	}
	return rawMessage;
};

const isAbortError = (error) => error && error.name === 'AbortError';

const requestCancelExtract = async () => {
	if (!modalState.extracting) {
		close();
		return;
	}
	if (modalState.extractCanceling) {
		return;
	}
	const abortController = modalState.extractAbortController;
	const extractRunId = modalState.extractRunId;
	const ok = await showConfirm('正在解压, 是否取消?', {
		title: 'CANCEL EXTRACT',
		okText: 'CANCEL',
		cancelText: 'CONTINUE',
		tone: 'warning',
	});
	if (!ok || !modalState.extracting || modalState.extractCanceling || !isCurrentExtractTask(abortController, extractRunId)) {
		return;
	}
	modalState.extractCanceling = true;
	setExtractProgress({ text: '正在取消...', detail: '', error: '' });
	if (abortController) {
		abortController.abort();
	}
};

const requestClose = async () => {
	await requestCancelExtract();
};

const buildExtractDetailText = (payload) => {
	const current = Number(payload?.current);
	const total = Number(payload?.total);
	const processedBytes = Number(payload?.processed_bytes);
	const processedBytesText = String(payload?.processed_bytes_text || '').trim();
	const skipped = Number(payload?.skipped);
	const skippedText = Number.isFinite(skipped) && skipped > 0 ? `, 跳过 ${Math.max(0, skipped)} 个已存在文件` : '';
	if (Number.isFinite(current) && Number.isFinite(total) && total > 0) {
		return `${Math.max(0, current)} / ${Math.max(0, total)}${skippedText}`;
	}
	if (Number.isFinite(processedBytes) && processedBytes > 0) {
		return `已处理 ${processedBytesText || processedBytes}${skippedText}`;
	}
	const base = String(payload?.detail || '').trim();
	return `${base}${skippedText}`.replace(/^,\s*/, '');
};

const buildExtractStatusText = (payload, fallback = '正在解压...') => {
	const stage = String(payload?.stage || '').trim();
	if (stage === 'identifying') {
		return '正在分析压缩包...';
	}
	if (stage === 'extracting') {
		return '正在解压...';
	}
	if (stage === 'failed') {
		return '解压失败';
	}
	if (stage === 'completed') {
		return '解压完成';
	}
	return fallback;
};

const extractArchive = async () => {
	const instanceName = state.currentInstanceName;
	const entry = modalState.currentFileActionEntry;
	if (!instanceName || !entry?.path) {
		return;
	}
	if (modalState.extracting) {
		return;
	}
	if (dom.fileExtractModeCustom.checked) {
		const dirName = truncateTextInputValue(dom.fileExtractDirName, InputValidation.limits.fileName).trim();
		if (!dirName) {
			dom.fileExtractDirName.reportValidity();
			dom.fileExtractDirName.focus();
			return;
		}
	}
	modalState.extracting = true;
	modalState.extractCanceling = false;
	const abortController = new AbortController();
	const extractRunId = modalState.extractRunId + 1;
	modalState.extractRunId = extractRunId;
	modalState.extractAbortController = abortController;
	setExtractProgress({ percent: 0, text: '正在解压...', detail: '正在连接任务流...', error: '' });
	const targetPath = InputValidation.truncateText(getExtractTargetPath(), InputValidation.limits.instancePath);
	try {
		let skippedFiles = 0;
		const res = await streamFileExtractAction(instanceName, {
			path: entry.path,
			target_path: targetPath,
			extract_here: !dom.fileExtractModeCustom.checked,
			overwrite: !!dom.fileExtractOverwrite.checked,
		}, { signal: abortController.signal });
		await readJsonSSEStream(res, {
			parseErrorMessage: '文件解压事件解析失败',
			onEvent: (name, payload) => {
				if (!isCurrentExtractTask(abortController, extractRunId)) {
					return;
				}
				const nextPercent = Number(payload?.percent);
				if (name === 'progress' || name === 'message') {
					setExtractProgress({
						percent: Number.isFinite(nextPercent) ? nextPercent : modalState.extractProgress.percent,
						text: buildExtractStatusText(payload, '正在解压...'),
						detail: buildExtractDetailText(payload),
						error: '',
					});
					return;
				}
				if (name === 'fail' || name === 'error') {
					const reason = String(payload?.reason || payload?.error || '解压失败').trim() || '解压失败';
					setExtractProgress({
						percent: Number.isFinite(nextPercent) ? nextPercent : modalState.extractProgress.percent,
						text: buildExtractStatusText(payload, '解压失败'),
						detail: buildExtractDetailText(payload),
						error: reason,
					});
					return;
				}
				if (name === 'end' || name === 'done') {
					skippedFiles = Math.max(0, Number(payload?.skipped) || 0);
					setExtractProgress({
						percent: Number.isFinite(nextPercent) ? nextPercent : 100,
						text: buildExtractStatusText(payload, '解压完成'),
						detail: buildExtractDetailText(payload),
						error: String(payload?.error || '').trim(),
					});
				}
			},
		});
		if (!isCurrentExtractTask(abortController, extractRunId)) {
			return;
		}
		if (modalState.extractProgress.error) {
			return;
		}
		setExtractProgress({ percent: 100, text: '解压完成' });
		if (typeof modalState.onRequestReload === 'function') {
			await modalState.onRequestReload();
		}
		if (!isCurrentExtractTask(abortController, extractRunId)) {
			return;
		}
		if (skippedFiles > 0) {
			return;
		}
		close();
	} catch (e) {
		if (!isCurrentExtractTask(abortController, extractRunId)) {
			return;
		}
		if (isAbortError(e)) {
			setExtractProgress({
				text: '解压已取消',
				detail: '',
				error: '',
			});
			return;
		}
		setExtractProgress({
			text: '解压失败',
			detail: '',
			error: extractErrorMessage(e),
		});
	} finally {
		if (isCurrentExtractTask(abortController, extractRunId)) {
			modalState.extractAbortController = null;
			modalState.extracting = false;
			modalState.extractCanceling = false;
			setExtractProgress();
		}
	}
};

const deleteFileEntry = async () => {
	const instanceName = state.currentInstanceName;
	const entry = modalState.currentFileActionEntry;
	if (!instanceName || !entry?.path) {
		return;
	}
	const targetName = String(entry.name || entry.path || '').trim() || '当前对象';
	const ok = await showConfirm(`确认删除 ${targetName}`, {
		title: 'DELETE',
		okText: 'DELETE',
		cancelText: 'CANCEL',
		tone: 'danger',
	});
	if (!ok) {
		return;
	}
	modalState.deleting = true;
	if (dom.fileDeleteSubmit) {
		dom.fileDeleteSubmit.textContent = 'DELETING...';
	}
	try {
		const result = await deleteFile(instanceName, entry.path);
		if (!result?.ok || !result.data) {
			if (result?.unauthorized) {
				return;
			}
			await showAlert(`删除失败: ${result?.error || '操作失败'}`, { title: 'ERROR', tone: 'danger' });
			return;
		}
		const data = result.data;
		if (typeof modalState.onApplyFileList === 'function') {
			modalState.onApplyFileList(data);
		} else if (typeof modalState.onRequestReload === 'function') {
			await modalState.onRequestReload();
		}
		close();
	} finally {
		modalState.deleting = false;
		if (dom.fileDeleteSubmit) {
			dom.fileDeleteSubmit.textContent = 'DELETE';
		}
	}
};

const close = () => {
    if (!dom.fileActionModal) return;
	if (modalState.extractAbortController) {
		modalState.extractAbortController.abort();
		modalState.extractAbortController = null;
	}
	modalState.extracting = false;
	modalState.extractCanceling = false;
    dom.fileActionModal.classList.remove('visible');
    dom.fileActionModal.classList.add('closing');
    modalState.fileActionModalCloseTimer = setTimeout(() => {
        dom.fileActionModal.style.display = 'none';
        dom.fileActionModal.classList.remove('closing');
		modalState.fileActionModalCloseTimer = null;
		modalState.currentFileActionEntry = null;
		modalState.initialPage = 'info';
		modalState.isArchive = false;
		modalState.deleting = false;
		modalState.downloading = false;
		modalState.extracting = false;
		modalState.extractAbortController = null;
		modalState.extractCanceling = false;
    }, 280);
};

const open = (entryOrOptions) => {
	const entry = entryOrOptions?.entry || entryOrOptions;
	if (!dom.fileActionModal || !entry) return;

    const entryName = entry.name || '';
    modalState.initialPage = String(entryOrOptions?.page || 'info');
    modalState.fileActionModalCloseTimer = clearTimer(modalState.fileActionModalCloseTimer);
    modalState.currentFileActionEntry = entry;
	const shouldFocusRename = !!dom.fileRenameName;
	if (dom.fileActionTitle) {
		dom.fileActionTitle.innerText = entry.isDir ? 'DIR' : 'FILE';
	}
	modalState.isArchive = !entry.isDir && getFileType(entryName, false) === 'zip';
	if (dom.fileActionInfoParentDir) {
		const parentDir = getEntryParentDirPath(entry);
		dom.fileActionInfoParentDir.innerText = parentDir || '-';
		if (dom.fileExtractSourcePath) {
			dom.fileExtractSourcePath.innerText = entry.path || '-';
		}
	}
	if (dom.fileActionInfoSize) {
		dom.fileActionInfoSize.innerText = entry.isDir ? '-' : formatFileSize(entry.size || 0);
	}
    if (dom.fileActionInfoModified) {
        dom.fileActionInfoModified.innerText = entry.modTime || '-';
    }
	if (dom.fileRenameName) {
		dom.fileRenameName.value = entryName;
	}
	if (dom.fileActionDownload) {
		dom.fileActionDownload.style.display = '';
		dom.fileActionDownload.textContent = 'DOWNLOAD';
	}
	if (dom.fileActionTabs) {
		dom.fileActionTabs.classList.remove('hidden');
	}
	if (dom.fileActionTabExtract) {
		dom.fileActionTabExtract.classList.toggle('hidden', !modalState.isArchive);
	}
	if (dom.fileActionPageExtract) {
		dom.fileActionPageExtract.classList.toggle('hidden', !modalState.isArchive);
	}
	if (dom.fileDeleteTarget) {
		dom.fileDeleteTarget.innerText = entry.path || entryName || '-';
	}
	if (dom.fileDeleteSubmit) {
		dom.fileDeleteSubmit.textContent = 'DELETE';
	}
	if (dom.fileExtractModeCurrent) {
		dom.fileExtractModeCurrent.checked = false;
	}
	if (dom.fileExtractModeCustom) {
		dom.fileExtractModeCustom.checked = true;
	}
	if (dom.fileExtractOverwrite) {
		dom.fileExtractOverwrite.checked = false;
	}
	if (dom.fileExtractDirName) {
		dom.fileExtractDirName.value = stripArchiveExtension(entryName);
	}
	resetExtractState();
	updateExtractTargetMode();
	applyFileActionPage(resolveOpenPage(modalState.initialPage));
    dom.fileActionModal.style.display = 'flex';
    dom.fileActionModal.classList.remove('closing');
    requestAnimationFrame(() => {
        dom.fileActionModal.classList.add('visible');
		if (shouldFocusRename && modalState.currentFileActionPage === 'info') {
			focusRenameInput();
		}
		if (modalState.currentFileActionPage === 'extract' && dom.fileExtractModeCustom.checked) {
			dom.fileExtractDirName.focus();
		}
    });
};

const renameFileEntry = async () => {
	const instanceName = state.currentInstanceName;
	const entry = modalState.currentFileActionEntry;
	if (!instanceName || !entry?.path) {
		return;
	}

	const newName = truncateTextInputValue(dom.fileRenameName, InputValidation.limits.fileName).trim();
	if (!newName) {
		await showAlert('名称不能为空', { title: 'INPUT' });
		dom.fileRenameName.focus();
		return;
	}
	if (newName === (entry.name || '')) {
		close();
		return;
	}

	const result = await renameFile(instanceName, entry.path, newName);
	if (!result?.ok || !result.data) {
		if (result?.unauthorized) {
			return;
		}
		await showAlert(`重命名失败: ${result?.error || '操作失败'}`, { title: 'ERROR', tone: 'danger' });
		return;
	}
	const data = result.data;
	if (data) {
		if (typeof modalState.onApplyFileList === 'function') {
			modalState.onApplyFileList(data);
		}
		close();
	}
};

const openFileDownloadPage = () => {
	const entry = modalState.currentFileActionEntry;
	if (!entry || entry.isDir) {
		return;
	}
	const instanceName = String(state.currentInstanceName || '').trim();
	const path = String(entry.path || '').trim();
	if (!instanceName || !path) {
		return;
	}
	const url = buildAuthedFileRawUrl(instanceName, path, { download: true });
	if (!url) {
		return;
	}
	triggerSilentDownload(url);
};

const normalizeArchiveRule = (entry) => {
	const path = String(entry.path || '').trim();
	if (!path) {
		return null;
	}
	return {
		path,
		is_dir: !!entry.isDir,
	};
};

const downloadDirectoryArchive = async () => {
	const entry = modalState.currentFileActionEntry;
	if (!entry || !entry.isDir) {
		return;
	}
	const instanceName = String(state.currentInstanceName || '').trim();
	const rule = normalizeArchiveRule(entry);
	if (!instanceName || !rule) {
		return;
	}
	modalState.downloading = true;
	if (dom.fileActionDownload) {
		dom.fileActionDownload.textContent = 'DOWNLOADING...';
	}
	try {
		const fallbackName = `${String(entry.name || 'archive').trim() || 'archive'}.zip`;
		const result = await downloadFileArchive(instanceName, [rule], [], fallbackName);
		if (!result.ok) {
			if (result.unauthorized) {
				return;
			}
			await showAlert(`下载失败: ${result.error || '操作失败'}`, { title: 'ERROR', tone: 'danger' });
		}
	} finally {
		modalState.downloading = false;
		if (dom.fileActionDownload) {
			dom.fileActionDownload.textContent = 'DOWNLOAD';
		}
	}
};

const downloadCurrentEntry = async () => {
	const entry = modalState.currentFileActionEntry;
	if (!entry) {
		return;
	}
	if (!entry.isDir) {
		openFileDownloadPage();
		return;
	}
	await downloadDirectoryArchive();
};

const bindEvents = () => {
    if (modalState.isBound) {
        return;
    }
    modalState.isBound = true;

    if (dom.fileActionClose) {
        dom.fileActionClose.onclick = () => requestClose();
    }
    if (dom.fileActionCancel) {
        dom.fileActionCancel.onclick = () => requestClose();
    }
	if (dom.fileActionDownload) {
		dom.fileActionDownload.onclick = async () => {
			await withActionsDisabled(dom.fileInfoActions, downloadCurrentEntry);
		};
	}
	bindTabPageButtons(fileActionTabPages, (page) => {
			if (modalState.extracting) {
				return;
			}
		if (page === 'extract') {
			if (!modalState.isArchive) {
				return;
			}
			applyFileActionPage(page);
			if (dom.fileExtractModeCustom.checked) {
				dom.fileExtractDirName.focus();
			}
			return;
		}
		applyFileActionPage(page === 'delete' ? 'delete' : 'info');
	});
	dom.fileExtractModeCurrent.addEventListener('change', () => updateExtractTargetMode());
	dom.fileExtractModeCustom.addEventListener('change', () => {
		updateExtractTargetMode();
		if (dom.fileExtractModeCustom.checked) {
			dom.fileExtractDirName.focus();
		}
	});
	dom.fileRenameName.addEventListener('blur', () => truncateTextInputValue(dom.fileRenameName, InputValidation.limits.fileName));
	dom.fileExtractDirName.addEventListener('blur', () => truncateTextInputValue(dom.fileExtractDirName, InputValidation.limits.fileName));
	if (dom.fileExtractCancel) {
		dom.fileExtractCancel.onclick = () => requestCancelExtract();
	}
	if (dom.fileDeleteCancel) {
		dom.fileDeleteCancel.onclick = () => requestClose();
	}
	if (dom.fileActionPageInfo) {
		dom.fileActionPageInfo.onsubmit = async (event) => {
            event.preventDefault();
			await withActionsDisabled(dom.fileInfoActions, renameFileEntry);
        };
    }
	if (dom.fileActionPageExtract) {
		dom.fileActionPageExtract.onsubmit = async (event) => {
			event.preventDefault();
			await extractArchive();
		};
	}
	if (dom.fileActionPageDelete) {
		dom.fileActionPageDelete.onsubmit = async (event) => {
			event.preventDefault();
			await withActionsDisabled(dom.fileDeleteActions, deleteFileEntry);
		};
	}
};

export const bootFileActionModal = (options = {}) => {
    modalState.onApplyFileList = options.onApplyFileList || null;
	modalState.onRequestReload = options.onRequestReload || null;
	modalState.getCurrentDir = options.getCurrentDir || null;
    bindEvents();
    return {
        open,
        close,
    };
};
