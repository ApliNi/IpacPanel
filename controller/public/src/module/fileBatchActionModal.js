import { mainModalOverlay, state } from "../ui.js";
import { clearTimer, withActionsDisabled } from '../utils/utils.js';
import { downloadFileArchive, streamFileBatchAction } from '../api/file.js';
import { showAlert } from './dialog.js';
import { InputValidation } from '../utils/inputValidation.js';
import { setupAutoResizeTextarea } from '../utils/autoTextarea.js';
import { readJsonSSEStream } from '../utils/sse.js';
import { applyTabPageState, bindTabPageButtons } from '../utils/modalHelper.js';

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="fileBatchActionModal" class="modal-overlay">
		<div class="modal-card file-batch-action-modal-card">
			<div class="modal-header">
				<span id="fileBatchActionTitle" class="modal-title">BATCH ACTION</span>
				<button id="fileBatchActionClose" class="modal-close" type="button">×</button>
			</div>
			<div class="modal-form file-batch-action-form">
				<div class="filter-group file-action-tabs file-batch-action-tabs">
					<button id="fileBatchTabCopy" class="filter-btn active" type="button" data-page="copy">COPY</button>
					<button id="fileBatchTabMove" class="filter-btn" type="button" data-page="move">MOVE</button>
					<button id="fileBatchTabDelete" class="filter-btn" type="button" data-page="delete">DELETE</button>
				</div>
				<div class="file-batch-action-body">
					<div id="fileBatchPageCopy" class="modal-page file-batch-action-page active">
						<div class="field-group">
							<span>TARGET DIR</span>
							<div id="fileBatchCopyTarget" class="file-action-static"></div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span id="fileBatchCopyRulesLabel">RULES</span>
							<textarea id="fileBatchCopyRules" class="input auto-textarea file-batch-rule-box" rows="4" maxlength="10485760" spellcheck="false"></textarea>
						</div>
						<div class="file-batch-copy-options">
							<label class="checkbox-group">
								<input id="fileBatchCopyOverwrite" type="checkbox">
								<span>覆盖已存在文件</span>
							</label>
							<span class="file-batch-copy-option-sep" aria-hidden="true">|</span>
							<label class="checkbox-group">
								<input id="fileBatchCopyDuplicate" type="checkbox">
								<span>创建副本</span>
							</label>
						</div>
					</div>
					<div id="fileBatchPageMove" class="modal-page file-batch-action-page">
						<div class="field-group">
							<span>TARGET DIR</span>
							<div id="fileBatchMoveTarget" class="file-action-static"></div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span id="fileBatchMoveRulesLabel">RULES</span>
							<textarea id="fileBatchMoveRules" class="input auto-textarea file-batch-rule-box" rows="4" maxlength="10485760" spellcheck="false"></textarea>
						</div>
						<label class="checkbox-group">
							<input id="fileBatchMoveOverwrite" type="checkbox">
							<span>覆盖已存在文件</span>
						</label>
					</div>
					<div id="fileBatchPageDelete" class="modal-page file-batch-action-page">
						<div class="field-group">
							<span>WARNING</span>
							<div class="file-action-static file-delete-warning">此操作不可撤销, 确认删除已选对象</div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span id="fileBatchDeleteRulesLabel">RULES</span>
							<textarea id="fileBatchDeleteRules" class="input auto-textarea file-batch-rule-box" rows="4" maxlength="10485760" spellcheck="false"></textarea>
						</div>
					</div>

					<div class="modal-actions modal-actions-split">
						<div class="modal-actions-group">
							<button class="btn" type="button" id="fileBatchDownload">DOWNLOAD</button>
						</div>
						<div class="modal-actions-group">
							<button class="btn" type="button" id="fileBatchCancel">CANCEL</button>
							<button class="btn btn-start" type="button" id="fileBatchSubmit">RUN</button>
						</div>
					</div>
				</div>
			</div>
		</div>
	</div>
`);

const dom = {
	modal: document.getElementById('fileBatchActionModal'),
	close: document.getElementById('fileBatchActionClose'),
	tabCopy: document.getElementById('fileBatchTabCopy'),
	tabMove: document.getElementById('fileBatchTabMove'),
	tabDelete: document.getElementById('fileBatchTabDelete'),
	pageCopy: document.getElementById('fileBatchPageCopy'),
	pageMove: document.getElementById('fileBatchPageMove'),
	pageDelete: document.getElementById('fileBatchPageDelete'),
	copyRulesLabel: document.getElementById('fileBatchCopyRulesLabel'),
	copyRules: document.getElementById('fileBatchCopyRules'),
	moveRulesLabel: document.getElementById('fileBatchMoveRulesLabel'),
	moveRules: document.getElementById('fileBatchMoveRules'),
	deleteRulesLabel: document.getElementById('fileBatchDeleteRulesLabel'),
	deleteRules: document.getElementById('fileBatchDeleteRules'),
	copyTarget: document.getElementById('fileBatchCopyTarget'),
	moveTarget: document.getElementById('fileBatchMoveTarget'),
	copyOverwrite: document.getElementById('fileBatchCopyOverwrite'),
	copyDuplicate: document.getElementById('fileBatchCopyDuplicate'),
	moveOverwrite: document.getElementById('fileBatchMoveOverwrite'),
	download: document.getElementById('fileBatchDownload'),
	cancel: document.getElementById('fileBatchCancel'),
	submit: document.getElementById('fileBatchSubmit'),
	actions: document.querySelector('#fileBatchActionModal .modal-actions'),
};

const modalState = {
	closeTimer: null,
	page: 'copy',
	onRequestReload: null,
	getCurrentDir: null,
	fileSelection: null,
	failed: [],
	okCount: 0,
	failCount: 0,
	submitMode: 'run',
	isBound: false,
};

const getCurrentDir = () => {
	if (typeof modalState.getCurrentDir === 'function') {
		return String(modalState.getCurrentDir() || '');
	}
	return '';
};

const fileBatchTabPages = [
	{ name: 'copy', tab: dom.tabCopy, page: dom.pageCopy },
	{ name: 'move', tab: dom.tabMove, page: dom.pageMove },
	{ name: 'delete', tab: dom.tabDelete, page: dom.pageDelete },
];

const getActiveRulesBox = () => {
	return modalState.page === 'move'
		? dom.moveRules
		: (modalState.page === 'delete' ? dom.deleteRules : dom.copyRules);
};

const resizeActiveRulesBox = () => {
	setupAutoResizeTextarea(getActiveRulesBox())();
};

const updateSubmitText = () => {
	if (!dom.submit || modalState.submitMode !== 'run') {
		return;
	}
	dom.submit.textContent = 'RUN';
};

const applyPage = (page) => {
	modalState.page = page;
	applyTabPageState(page, fileBatchTabPages);
	if (page === 'copy' && dom.copyOverwrite) {
		dom.copyOverwrite.checked = false;
	}
	if (page === 'copy' && dom.copyDuplicate) {
		dom.copyDuplicate.checked = false;
	}
	if (page === 'move' && dom.moveOverwrite) {
		dom.moveOverwrite.checked = false;
	}
	updateSubmitText();
	requestAnimationFrame(resizeActiveRulesBox);
};

const resetProgress = () => {
	modalState.failed = [];
	modalState.okCount = 0;
	modalState.failCount = 0;
	updateRulesLabels();
	modalState.submitMode = 'run';
	updateSubmitText();
};

const updateTargets = () => {
	const dir = getCurrentDir().trim();
	const display = dir ? `./${dir}/` : './';
	if (dom.copyTarget) dom.copyTarget.textContent = display;
	if (dom.moveTarget) dom.moveTarget.textContent = display;
};

const formatRulePath = (path, isDir) => {
	let rel = String(path || '').trim().replaceAll('\\', '/').replace(/^\/+/, '').replace(/\/+$/g, '');
	if (!rel) {
		return './';
	}
	if (isDir) {
		return `./${rel}/`;
	}
	return `./${rel}`;
};

const buildRulesText = () => {
	const sel = modalState.fileSelection?.getSelection?.() || {};
	const include = Array.isArray(sel.include) ? sel.include : [];
	const exclude = Array.isArray(sel.exclude) ? sel.exclude : [];
	const lines = [];
	include.forEach((r) => {
		const p = String(r?.path || '').trim();
		if (!p) return;
		const isDir = !!(r?.is_dir ?? r?.isDir);
		lines.push(`[+] ${formatRulePath(p, isDir)}`);
	});
	exclude.forEach((r) => {
		const p = String(r?.path || '').trim();
		if (!p) return;
		const isDir = !!(r?.is_dir ?? r?.isDir);
		lines.push(`[-] ${formatRulePath(p, isDir)}`);
	});
	return lines.join('\n');
};

const updateRules = () => {
	const text = InputValidation.truncateUTF8Bytes(buildRulesText(), InputValidation.limits.fileContent);
	if (dom.copyRules) dom.copyRules.value = text;
	if (dom.moveRules) dom.moveRules.value = text;
	if (dom.deleteRules) dom.deleteRules.value = text;
	[dom.copyRules, dom.moveRules, dom.deleteRules].forEach((box) => setupAutoResizeTextarea(box)());
};

const truncateRuleBox = (box) => {
	if (box) {
		box.value = InputValidation.truncateUTF8Bytes(box.value || '', InputValidation.limits.fileContent);
		setupAutoResizeTextarea(box)();
	}
};

const getNormalizedSelectionRules = async () => {
	const selection = modalState.fileSelection?.getSelection?.() || {};
	const includeRaw = Array.isArray(selection.include) ? selection.include : [];
	const excludeRaw = Array.isArray(selection.exclude) ? selection.exclude : [];
	const normalizeRule = (r) => {
		const path = String(r?.path || '').trim();
		if (!path) return null;
		return {
			path,
			is_dir: !!(r?.is_dir ?? r?.isDir),
		};
	};
	const include = includeRaw.map(normalizeRule).filter(Boolean);
	const exclude = excludeRaw.map(normalizeRule).filter(Boolean);
	if (!include.length && !exclude.length) {
		await showAlert('未选择任何规则', { title: 'INPUT' });
		return null;
	}
	return { include, exclude };
};

const close = () => {
	if (!dom.modal) return;
	dom.modal.classList.remove('visible');
	dom.modal.classList.add('closing');
	modalState.closeTimer = setTimeout(() => {
		dom.modal.style.display = 'none';
		dom.modal.classList.remove('closing');
		modalState.closeTimer = null;
	}, 280);
};

const open = () => {
	if (!dom.modal) return;
	modalState.closeTimer = clearTimer(modalState.closeTimer);
	resetProgress();
	updateRules();
	updateTargets();
	if (dom.copyOverwrite) dom.copyOverwrite.checked = false;
	if (dom.copyDuplicate) dom.copyDuplicate.checked = false;
	if (dom.moveOverwrite) dom.moveOverwrite.checked = false;
	applyPage('copy');
	dom.modal.style.display = 'flex';
	dom.modal.classList.remove('closing');
	requestAnimationFrame(() => {
		dom.modal.classList.add('visible');
		resizeActiveRulesBox();
	});
};

const updateRulesLabels = () => {
	if (modalState.okCount <= 0 && modalState.failCount <= 0) {
		if (dom.copyRulesLabel) dom.copyRulesLabel.textContent = 'RULES';
		if (dom.moveRulesLabel) dom.moveRulesLabel.textContent = 'RULES';
		if (dom.deleteRulesLabel) dom.deleteRulesLabel.textContent = 'RULES';
		return;
	}
	const okCount = Math.max(0, Number(modalState.okCount || 0));
	const failCount = Math.max(0, Number(modalState.failCount || 0));
	const suffix = `SUCCESS ${okCount} / FAILED ${failCount}`;
	if (dom.copyRulesLabel) dom.copyRulesLabel.textContent = `RULES [${suffix}]`;
	if (dom.moveRulesLabel) dom.moveRulesLabel.textContent = `RULES [${suffix}]`;
	if (dom.deleteRulesLabel) dom.deleteRulesLabel.textContent = `RULES [${suffix}]`;
};
const appendFail = (path, reason, isDir, rulesBox = getActiveRulesBox()) => {
	const p = String(path || '').trim();
	if (!p) return;
	const r = String(reason || '').trim() || '失败';
	modalState.failed.push({ path: p, reason: r, isDir: !!isDir });
	modalState.failCount += 1;
	updateRulesLabels();
	const dir = (typeof isDir === 'boolean') ? isDir : (p.endsWith('/') || p.endsWith('\\'));
	const rel = formatRulePath(p, dir);
	const line = `[${r}] ${rel}`;
	const box = rulesBox;
	if (box) {
		box.value = InputValidation.truncateUTF8Bytes(`${box.value || ''}${box.value ? '\n' : ''}${line}`, InputValidation.limits.fileContent);
		setupAutoResizeTextarea(box)();
	}
};

const setCounts = ({ ok, fail }) => {
	if (typeof ok === 'number') modalState.okCount = Math.max(0, ok);
	if (typeof fail === 'number') modalState.failCount = Math.max(0, fail);
	updateRulesLabels();
};

const selectAllFailedFilesAndClose = () => {
	const map = new Map();
	modalState.failed.forEach((f) => {
		const p = String(f?.path || '').trim();
		if (!p) return;
		if (!map.has(p)) {
			map.set(p, { path: p, isDir: !!f?.isDir });
		}
	});
	const failedItems = Array.from(map.values());
	modalState.fileSelection?.setSelection?.({
		include: failedItems.map((it) => ({ path: it.path, is_dir: it.isDir })),
		exclude: [],
	});
	if (typeof modalState.onRequestReload === 'function') {
		modalState.onRequestReload();
	}
	close();
};

const clearSelectionAfterRun = () => {
	modalState.fileSelection?.clearSelection?.();
};

const runBatch = async ({ action, overwrite }) => {
	const instanceName = state.currentInstanceName;
	if (!instanceName) {
		await showAlert('实例未选择', { title: 'ERROR', tone: 'danger' });
		return;
	}
	const rules = await getNormalizedSelectionRules();
	if (!rules) {
		return;
	}
	const { include, exclude } = rules;
	resetProgress();
	// Before running, clear log area (current page rules box will be used as error output).
	const box = getActiveRulesBox();
	if (box) {
		box.value = '';
		setupAutoResizeTextarea(box)();
	}

	const res = await streamFileBatchAction(instanceName, {
		action,
		dest_dir: getCurrentDir(),
		overwrite: !!overwrite,
		copy_duplicate: action === 'copy' && !!dom.copyDuplicate.checked,
		include,
		exclude,
	});

	try {
		await readJsonSSEStream(res, {
			parseErrorMessage: '文件批量操作事件解析失败',
			eventTypes: ['message', 'progress', 'fail', 'end'],
			onEvent: (name, payload) => {
				if (name === 'progress') {
					setCounts({ ok: Number(payload?.ok || 0), fail: Number(payload?.fail || 0) });
					return;
				}
				if (name === 'fail') {
					appendFail(payload?.path, payload?.reason, payload?.is_dir, box);
					return;
				}
				if (name === 'end') {
					setCounts({ ok: Number(payload?.ok || 0), fail: Number(payload?.fail || 0) });
					clearSelectionAfterRun();
					if (modalState.failed.length > 0) {
						modalState.submitMode = 'select_failed';
						if (dom.submit) dom.submit.textContent = 'RECOVER SELECT';
						return;
					}
					// Auto-close when everything succeeds.
					if (typeof modalState.onRequestReload === 'function') {
						modalState.onRequestReload();
					}
					close();
				}
			},
		});
	} catch (error) {
		appendFail(getCurrentDir() || './', error?.message || '操作失败', true, box);
	}
};

const downloadBatchArchive = async () => {
	const instanceName = state.currentInstanceName;
	if (!instanceName) {
		await showAlert('实例未选择', { title: 'ERROR', tone: 'danger' });
		return;
	}
	const rules = await getNormalizedSelectionRules();
	if (!rules) {
		return;
	}
	const result = await downloadFileArchive(instanceName, rules.include, rules.exclude, 'archive.zip');
	if (!result.ok) {
		if (result.unauthorized) {
			return;
		}
		await showAlert(`下载失败: ${result.error || '操作失败'}`, { title: 'ERROR', tone: 'danger' });
	}
};

const bindEvents = () => {
	if (modalState.isBound) return;
	modalState.isBound = true;

	dom.close.addEventListener('click', () => close());
	dom.cancel.addEventListener('click', () => close());

	bindTabPageButtons(fileBatchTabPages, applyPage);
	[dom.copyRules, dom.moveRules, dom.deleteRules].forEach((box) => {
		setupAutoResizeTextarea(box);
		box.addEventListener('blur', () => truncateRuleBox(box));
	});
	dom.copyDuplicate.addEventListener('change', () => {
		if (dom.copyDuplicate.checked && dom.copyOverwrite) {
			dom.copyOverwrite.checked = false;
		}
	});
	dom.copyOverwrite.addEventListener('change', () => {
		if (dom.copyOverwrite.checked && dom.copyDuplicate) {
			dom.copyDuplicate.checked = false;
		}
	});
	dom.download.addEventListener('click', async () => {
		await withActionsDisabled(dom.actions, downloadBatchArchive);
	});

	dom.submit.addEventListener('click', async () => {
		[dom.copyRules, dom.moveRules, dom.deleteRules].forEach(truncateRuleBox);
		if (modalState.submitMode === 'select_failed') {
			selectAllFailedFilesAndClose();
			return;
		}
		const page = modalState.page || 'copy';
		const action = page === 'move' ? 'move' : (page === 'delete' ? 'delete' : 'copy');
		const overwrite = action === 'delete' ? false : (action === 'move' ? !!dom.moveOverwrite.checked : !!dom.copyOverwrite.checked);
		await withActionsDisabled(dom.actions, () => runBatch({ action, overwrite }));
	});
};

export const bootFileBatchActionModal = (options = {}) => {
	modalState.onRequestReload = options.onRequestReload || null;
	modalState.getCurrentDir = options.getCurrentDir || null;
	modalState.fileSelection = options.fileSelection || null;
	bindEvents();
	return {
		open,
		close,
	};
};
