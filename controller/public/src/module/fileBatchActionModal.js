import { mainModalOverlay, state } from "../ui.js";
import { clearTimer, withActionsDisabled } from '../utils/utils.js';
import { streamFileBatchAction } from '../api/file.js';
import { showAlert } from './dialog.js';
import { InputValidation } from '../utils/inputValidation.js';

console.log('[模块] FileBatchActionModal 加载中...');

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
					<div id="fileBatchPageCopy" class="file-batch-action-page active">
						<div class="field-group">
							<span>TARGET DIR</span>
							<div id="fileBatchCopyTarget" class="file-action-static"></div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span id="fileBatchCopyRulesLabel">RULES</span>
							<div id="fileBatchCopyRules" class="text-editable input file-batch-rule-box" contenteditable="plaintext-only" spellcheck="false"></div>
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
					<div id="fileBatchPageMove" class="file-batch-action-page">
						<div class="field-group">
							<span>TARGET DIR</span>
							<div id="fileBatchMoveTarget" class="file-action-static"></div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span id="fileBatchMoveRulesLabel">RULES</span>
							<div id="fileBatchMoveRules" class="text-editable input file-batch-rule-box" contenteditable="plaintext-only" spellcheck="false"></div>
						</div>
						<label class="checkbox-group">
							<input id="fileBatchMoveOverwrite" type="checkbox">
							<span>覆盖已存在文件</span>
						</label>
					</div>
					<div id="fileBatchPageDelete" class="file-batch-action-page">
						<div class="field-group">
							<span>WARNING</span>
							<div class="file-action-static file-delete-warning">此操作不可撤销, 确认删除已选对象</div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span id="fileBatchDeleteRulesLabel">RULES</span>
							<div id="fileBatchDeleteRules" class="text-editable input file-batch-rule-box" contenteditable="plaintext-only" spellcheck="false"></div>
						</div>
					</div>

					<div class="modal-actions">
						<button class="btn" type="button" id="fileBatchCancel">CANCEL</button>
						<button class="btn btn-start" type="button" id="fileBatchSubmit">RUN</button>
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

const applyPage = (page) => {
	modalState.page = page;
	dom.tabCopy?.classList.toggle('active', page === 'copy');
	dom.tabMove?.classList.toggle('active', page === 'move');
	dom.tabDelete?.classList.toggle('active', page === 'delete');
	dom.pageCopy?.classList.toggle('active', page === 'copy');
	dom.pageMove?.classList.toggle('active', page === 'move');
	dom.pageDelete?.classList.toggle('active', page === 'delete');
	if (page === 'copy' && dom.copyOverwrite) {
		dom.copyOverwrite.checked = false;
	}
	if (page === 'copy' && dom.copyDuplicate) {
		dom.copyDuplicate.checked = false;
	}
	if (page === 'move' && dom.moveOverwrite) {
		dom.moveOverwrite.checked = false;
	}
};

const resetProgress = () => {
	modalState.failed = [];
	modalState.okCount = 0;
	modalState.failCount = 0;
	updateRulesLabels();
	modalState.submitMode = 'run';
	if (dom.submit) dom.submit.textContent = 'RUN';
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
	if (dom.copyRules) dom.copyRules.textContent = text;
	if (dom.moveRules) dom.moveRules.textContent = text;
	if (dom.deleteRules) dom.deleteRules.textContent = text;
};

const truncateRuleBox = (box) => {
	if (box) {
		box.textContent = InputValidation.truncateUTF8Bytes(box.textContent || '', InputValidation.limits.fileContent);
	}
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



const getActiveRulesBox = () => {
	return modalState.page === 'move'
		? dom.moveRules
		: (modalState.page === 'delete' ? dom.deleteRules : dom.copyRules);
};

const appendFail = (path, reason, isDir) => {
	const p = String(path || '').trim();
	if (!p) return;
	const r = String(reason || '').trim() || '失败';
	modalState.failed.push({ path: p, reason: r, isDir: !!isDir });
	modalState.failCount += 1;
	updateRulesLabels();
	const dir = (typeof isDir === 'boolean') ? isDir : (p.endsWith('/') || p.endsWith('\\'));
	const rel = formatRulePath(p, dir);
	const line = `[${r}] ${rel}`;
	const box = getActiveRulesBox();
	if (box) {
		box.textContent = InputValidation.truncateUTF8Bytes(`${box.textContent || ''}${box.textContent ? '\n' : ''}${line}`, InputValidation.limits.fileContent);
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

const decodeSse = async (res, handlers) => {
	const reader = res.body?.getReader?.();
	if (!reader) {
		throw new Error('stream not supported');
	}
	const decoder = new TextDecoder('utf-8');
	let buf = '';
	let eventName = '';
	let dataBuf = '';

	const emit = () => {
		const name = String(eventName || 'message');
		const raw = String(dataBuf || '').trim();
		if (!raw) {
			eventName = '';
			dataBuf = '';
			return;
		}
		let obj = null;
		try {
			obj = JSON.parse(raw);
		} catch (_) {
			obj = { raw };
		}
		if (typeof handlers?.onEvent === 'function') {
			handlers.onEvent(name, obj);
		}
		eventName = '';
		dataBuf = '';
	};

	while (true) {
		const { value, done } = await reader.read();
		if (done) break;
		buf += decoder.decode(value, { stream: true });
		let idx;
		while ((idx = buf.indexOf('\n')) >= 0) {
			let line = buf.slice(0, idx);
			buf = buf.slice(idx + 1);
			if (line.endsWith('\r')) line = line.slice(0, -1);
			if (!line) {
				emit();
				continue;
			}
			if (line.startsWith('event:')) {
				eventName = line.slice(6).trim();
				continue;
			}
			if (line.startsWith('data:')) {
				const chunk = line.slice(5).trim();
				dataBuf = dataBuf ? `${dataBuf}\n${chunk}` : chunk;
				continue;
			}
		}
	}
	// Flush any pending event on EOF.
	emit();
};

const runBatch = async ({ action, overwrite }) => {
	const instanceName = state.currentInstanceName;
	if (!instanceName) {
		await showAlert('实例未选择', { title: 'ERROR', tone: 'danger' });
		return;
	}
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
		return;
	}
	resetProgress();
	// Before running, clear log area (current page rules box will be used as error output).
	const box = getActiveRulesBox();
	if (box) box.textContent = '';

	const res = await streamFileBatchAction(instanceName, {
		action,
		dest_dir: getCurrentDir(),
		overwrite: !!overwrite,
		copy_duplicate: action === 'copy' && !!dom.copyDuplicate?.checked,
		include,
		exclude,
	});

	try {
		await decodeSse(res, {
		onEvent: (name, payload) => {
			if (name === 'progress') {
				setCounts({ ok: Number(payload?.ok || 0), fail: Number(payload?.fail || 0) });
				return;
			}
			if (name === 'fail') {
				appendFail(payload?.path, payload?.reason, payload?.is_dir);
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
		appendFail(getCurrentDir() || './', error?.message || '操作失败', true);
	}
};

const bindEvents = () => {
	if (modalState.isBound) return;
	modalState.isBound = true;

	dom.close?.addEventListener('click', () => close());
	dom.cancel?.addEventListener('click', () => close());

	dom.tabCopy?.addEventListener('click', () => applyPage('copy'));
	dom.tabMove?.addEventListener('click', () => applyPage('move'));
	dom.tabDelete?.addEventListener('click', () => applyPage('delete'));
	[dom.copyRules, dom.moveRules, dom.deleteRules].forEach((box) => {
		box?.addEventListener('blur', () => truncateRuleBox(box));
	});
	dom.copyDuplicate?.addEventListener('change', () => {
		if (dom.copyDuplicate?.checked && dom.copyOverwrite) {
			dom.copyOverwrite.checked = false;
		}
	});
	dom.copyOverwrite?.addEventListener('change', () => {
		if (dom.copyOverwrite?.checked && dom.copyDuplicate) {
			dom.copyDuplicate.checked = false;
		}
	});

	dom.submit?.addEventListener('click', async () => {
		[dom.copyRules, dom.moveRules, dom.deleteRules].forEach(truncateRuleBox);
		if (modalState.submitMode === 'select_failed') {
			selectAllFailedFilesAndClose();
			return;
		}
		const page = modalState.page || 'copy';
		const action = page === 'move' ? 'move' : (page === 'delete' ? 'delete' : 'copy');
		const overwrite = action === 'delete' ? false : (action === 'move' ? !!dom.moveOverwrite?.checked : !!dom.copyOverwrite?.checked);
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
