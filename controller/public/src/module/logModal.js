import { mainModalOverlay } from "../ui.js";
import { closeAnimatedModal, openAnimatedModal } from '../utils/utils.js';
import { logStore } from '../store/logStore.js';
import { instanceStatusStore } from '../store/instanceStatusStore.js';
import { clearLogs } from '../api/log.js';
import { showAlert, showConfirm } from './dialog.js';

const formatLogTime = (unixSeconds) => {
	const date = new Date((Number(unixSeconds) || 0) * 1000);
	const pad = (value) => String(value).padStart(2, '0');
	return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
};

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="logModal" class="modal-overlay">
		<div class="modal-card log-modal-card">
			<div class="modal-header">
				<span class="modal-title">LOG</span>
				<button id="logClose" class="modal-close" type="button">×</button>
			</div>
			<div class="modal-form log-form">
				<div class="filter-group log-tabs">
					<button id="logTabAll" class="filter-btn active" type="button" data-level="">ALL</button>
					<button id="logTabWarn" class="filter-btn" type="button" data-level="warn">WARN</button>
					<button id="logTabError" class="filter-btn" type="button" data-level="error">ERROR</button>
				</div>
				<div class="field-group">
					<div id="logList" class="log-list"></div>
				</div>
				<div class="modal-actions">
					<span id="logStatus" aria-live="polite"></span>
					<span class="controls-divider" aria-hidden="true">|</span>
					<button class="btn" type="button" id="logClear">CLEAR</button>
					<button class="btn btn-start" type="button" id="logCloseBtn">CLOSE</button>
				</div>
			</div>
		</div>
	</div>
`);

const dom = {
	modal: document.getElementById('logModal'),
	close: document.getElementById('logClose'),
	closeBtn: document.getElementById('logCloseBtn'),
	clear: document.getElementById('logClear'),
	status: document.getElementById('logStatus'),
	tabs: document.querySelectorAll('#logModal .log-tabs .filter-btn'),
	list: document.getElementById('logList'),
};

const modalState = {
	isBound: false,
	opened: false,
	levelFilter: '',
	unsubscribe: null,
	closeTimer: null,
};

// 由 bootLogModal 注入的实例打开回调 (ui.js 的 openInstanceTerminal).
let instanceOpenHandler = null;

const setLogStatus = (text, options = {}) => {
	dom.status.textContent = text;
	dom.status.classList.toggle('error', !!options.error);
};

const getFilteredEntries = () => {
	const entries = logStore.getSnapshot().entries;
	if (!modalState.levelFilter) {
		return entries;
	}
	if (modalState.levelFilter === 'warn') {
		return entries.filter((entry) => entry.level === 'warn' || entry.level === 'error');
	}
	return entries.filter((entry) => entry.level === 'error');
};

const buildEmptyNode = (text) => {
	const empty = document.createElement('div');
	empty.className = 'log-entry';
	empty.textContent = text;
	return empty;
};

const buildLogEntryNode = (entry) => {
	const row = document.createElement('div');
	row.className = `log-entry${entry.level === 'warn' ? ' log-entry-warn' : ''}${entry.level === 'error' ? ' log-entry-error' : ''}`;

	const time = document.createElement('span');
	time.className = 'log-time';
	time.textContent = formatLogTime(entry.time);

	const message = document.createElement('span');
	message.className = 'log-message';
	message.textContent = entry.message;
	row.appendChild(time);

	if (entry.instance) {
		// a 标签支持按住 shift/ctrl 在新标签页或窗口打开; 普通点击仍走当前页切换.
		const instanceTag = document.createElement('a');
		instanceTag.className = 'log-instance';
		instanceTag.textContent = `[${entry.instance}]`;
		instanceTag.title = '打开实例终端';
		instanceTag.href = `?i=${encodeURIComponent(entry.instance)}`;
		instanceTag.onclick = (event) => {
			if (event.shiftKey || event.ctrlKey || event.metaKey) {
				return;
			}
			event.preventDefault();
			void openInstanceByName(entry.instance);
		};
		row.appendChild(instanceTag);
	}

	row.appendChild(message);
	return row;
};

const renderList = () => {
	const entries = modalState.viewEntries;
	if (entries.length === 0) {
		dom.list.replaceChildren(buildEmptyNode('EMPTY'));
		return;
	}
	// 日志按 seq 升序存储; 展示时最新的在最上面.
	dom.list.replaceChildren(...entries.slice().reverse().map(buildLogEntryNode));
};

const refreshView = () => {
	modalState.viewEntries = getFilteredEntries();
	renderList();
};

const applyLevelFilter = (level) => {
	modalState.levelFilter = level;
	dom.tabs.forEach((btn) => btn.classList.toggle('active', (btn.dataset.level || '') === level));
	refreshView();
};

const openInstanceByName = async (instanceName) => {
	const ins = instanceStatusStore.getInstance(instanceName);
	if (!ins) {
		showAlert(`实例 ${instanceName} 不存在或无权访问`);
		return;
	}
	close();
	if (!instanceOpenHandler) {
		return;
	}
	await instanceOpenHandler(ins);
};

const clearAllLogs = async () => {
	const confirmed = await showConfirm('确认清空服务端运行日志缓冲?', { title: 'CLEAR', tone: 'danger', okText: 'CLEAR' });
	if (!confirmed) return;
	const result = await clearLogs();
	if (!result.ok) {
		setLogStatus(result.error || '清空运行日志失败', { error: true });
		return;
	}
	// resetLocal 同步归零本地计数, 驱动 LOG 按钮高亮消失.
	logStore.resetLocal();
	refreshView();
	setLogStatus('');
};

const bindEvents = () => {
	if (modalState.isBound) return;
	modalState.isBound = true;

	dom.close.onclick = () => close();
	dom.closeBtn.onclick = () => close();
	dom.clear.onclick = () => void clearAllLogs();
	dom.tabs.forEach((btn) => {
		btn.onclick = () => applyLevelFilter(btn.dataset.level || '');
	});
};

const open = async () => {
	bindEvents();
	modalState.opened = true;
	openAnimatedModal(dom.modal);
	// 打开时全量拉取服务端缓冲, 之后由 SSE 计数事件驱动按钮状态.
	setLogStatus('LOADING...');
	try {
		await logStore.loadAll();
		setLogStatus('');
		refreshView();
	} catch (error) {
		setLogStatus(error?.message || '获取运行日志失败', { error: true });
	}
};

const close = () => {
	modalState.opened = false;
	modalState.closeTimer = closeAnimatedModal(dom.modal, modalState.closeTimer, () => {}, 280);
};

export const bootLogModal = ({ onOpenInstance, onCountChange } = {}) => {
	if (typeof onOpenInstance === 'function') {
		instanceOpenHandler = onOpenInstance;
	}
	if (!modalState.unsubscribe) {
		modalState.unsubscribe = logStore.subscribe((snapshot) => {
			if (modalState.opened) {
				refreshView();
			}
			if (typeof onCountChange === 'function') {
				onCountChange(snapshot.count);
			}
		});
	}
	return { open, close };
};
