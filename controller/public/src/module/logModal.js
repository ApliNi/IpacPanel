import { mainModalOverlay } from "../ui.js";
import { closeAnimatedModal, openAnimatedModal } from '../utils/utils.js';
import { logStore } from '../store/logStore.js';
import { instanceStatusStore } from '../store/instanceStatusStore.js';
import { clearLogs } from '../api/log.js';
import { showAlert, showConfirm } from './dialog.js';

const PAGE_SIZE = 150;

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
	viewEntries: [],
	visibleLimit: PAGE_SIZE,
	isLoadingMore: false,
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
	empty.className = 'log-entry log-empty';
	empty.textContent = text;
	return empty;
};

const buildLogEntryNode = (entry) => {
	const row = document.createElement('div');
	row.className = `log-entry${entry.level === 'warn' ? ' log-entry-warn' : ''}${entry.level === 'error' ? ' log-entry-error' : ''}`;
	row.dataset.seq = String(entry.seq);

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

const renderFull = (entries) => {
	modalState.visibleLimit = PAGE_SIZE;
	if (entries.length === 0) {
		dom.list.replaceChildren(buildEmptyNode('EMPTY'));
		return;
	}
	// 日志按 seq 升序存储; 展示时最新的在最上面.
	const visible = entries.slice(-PAGE_SIZE).reverse();
	const fragment = document.createDocumentFragment();
	for (const entry of visible) {
		fragment.appendChild(buildLogEntryNode(entry));
	}
	dom.list.replaceChildren(fragment);
	dom.list.scrollTop = 0;
};

const renderList = (forceReset = false) => {
	const entries = modalState.viewEntries;
	if (entries.length === 0) {
		modalState.visibleLimit = PAGE_SIZE;
		dom.list.replaceChildren(buildEmptyNode('EMPTY'));
		return;
	}

	if (forceReset) {
		renderFull(entries);
		return;
	}

	const topElement = dom.list.firstElementChild;
	const topSeq = topElement ? Number(topElement.dataset?.seq) : NaN;
	if (Number.isNaN(topSeq)) {
		renderFull(entries);
		return;
	}

	const latestSeq = entries[entries.length - 1]?.seq;
	if (latestSeq != null && latestSeq < topSeq) {
		renderFull(entries);
		return;
	}

	const newEntries = [];
	for (let i = entries.length - 1; i >= 0; i--) {
		if (entries[i].seq > topSeq) {
			newEntries.push(entries[i]);
		} else {
			break;
		}
	}

	if (newEntries.length === 0) {
		return;
	}

	if (newEntries.length > modalState.visibleLimit) {
		renderFull(entries);
		return;
	}

	const fragment = document.createDocumentFragment();
	for (const entry of newEntries) {
		fragment.appendChild(buildLogEntryNode(entry));
	}

	const prevScrollTop = dom.list.scrollTop;
	const prevScrollHeight = dom.list.scrollHeight;
	dom.list.prepend(fragment);
	if (prevScrollTop > 10) {
		dom.list.scrollTop = prevScrollTop + (dom.list.scrollHeight - prevScrollHeight);
	} else {
		dom.list.scrollTop = 0;
		while (dom.list.children.length > modalState.visibleLimit) {
			dom.list.lastElementChild.remove();
		}
	}
};

const loadMoreOlderEntries = () => {
	if (modalState.isLoadingMore) return;
	modalState.isLoadingMore = true;
	try {
		const bottomElement = dom.list.lastElementChild;
		const bottomSeq = bottomElement ? Number(bottomElement.dataset?.seq) : NaN;
		if (Number.isNaN(bottomSeq)) return;
		const entries = modalState.viewEntries || [];

		let lastOlderIndex = -1;
		for (let i = entries.length - 1; i >= 0; i--) {
			if (entries[i].seq < bottomSeq) {
				lastOlderIndex = i;
				break;
			}
		}

		if (lastOlderIndex < 0) return;

		const startIndex = Math.max(0, lastOlderIndex - PAGE_SIZE + 1);
		const batch = entries.slice(startIndex, lastOlderIndex + 1).reverse();
		if (batch.length === 0) return;

		const fragment = document.createDocumentFragment();
		for (const entry of batch) {
			fragment.appendChild(buildLogEntryNode(entry));
		}
		dom.list.appendChild(fragment);
		modalState.visibleLimit += batch.length;
	} finally {
		modalState.isLoadingMore = false;
	}
};

const handleScroll = () => {
	if (modalState.isLoadingMore) return;
	const { scrollTop, scrollHeight, clientHeight } = dom.list;
	if (scrollTop + clientHeight >= scrollHeight - 80) {
		loadMoreOlderEntries();
	}
};

const refreshView = (forceReset = false) => {
	modalState.viewEntries = getFilteredEntries();
	renderList(forceReset);
};

const applyLevelFilter = (level) => {
	modalState.levelFilter = level;
	dom.tabs.forEach((btn) => btn.classList.toggle('active', (btn.dataset.level || '') === level));
	refreshView(true);
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
	refreshView(true);
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
	dom.list.addEventListener('scroll', handleScroll, { passive: true });
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
		refreshView(true);
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
				refreshView(false);
			}
			if (typeof onCountChange === 'function') {
				onCountChange(snapshot.count);
			}
		});
	}
	return { open, close };
};
