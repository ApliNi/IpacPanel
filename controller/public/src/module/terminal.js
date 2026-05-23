import { mainContainer, state } from "../ui.js";
import { DEFAULT_UI_REFRESH_INTERVAL_MS, clearTimer } from '../utils/utils.js';
import { controlInstance, createWebSocket } from '../api/instance.js';
import { normalizeTerminalMode, terminalMode } from '../utils/enum.js';
import { icons } from '../utils/icon.js';
import { InputValidation } from '../utils/inputValidation.js';
import { showConfirm } from './dialog.js';
const getTerminalRuntime = () => {
	const win = window;
	const TerminalCtor = win.Terminal;
	if (!TerminalCtor) throw new Error('xterm 运行时不可用');
	const FitAddon = win.FitAddon;
	if (!FitAddon || !FitAddon.FitAddon) throw new Error('xterm fit 插件构造函数不可用');
	return { TerminalCtor, FitAddonCtor: FitAddon.FitAddon };
};

console.log('[模块] TerminalWorkspace 加载中...');

mainContainer.insertAdjacentHTML('beforeend', /*html*/`
	<section id="terminalSection" class="section">
		<style>
			.file-pagination-status {
				display: flex;
				align-items: center;
				gap: 8px;
			}

			.file-pagination-input {
				width: 48px;
				padding: 3px 6px;
				text-align: center;
				line-height: 16px;
			}
		</style>
		<div class="console-content">
			<div class="card main-terminal-card">
				<div class="terminal-toolbar">
					<div class="info">
						<span id="termStatus" class="status">[OFFLINE]</span>
						<span id="termTitle" class="title">INSTANCE</span>
					</div>
					<div class="controls">
						<button id="ctrlKill" class="btn btn-kill">KILL</button>
						<button id="ctrlRestart" class="btn btn-restart">RESTART</button>
						<button id="ctrlStart" class="btn btn-start">START</button>
						<button id="ctrlStop" class="btn btn-stop">STOP</button>
						<span class="controls-divider" aria-hidden="true">|</span>
						<button id="editInstanceBtn" class="btn" type="button">CONFIG</button>
					</div>
				</div>
				<div class="terminal-wrapper">
					<div id="terminalDiv"></div>
				</div>
				<div id="terminalDisabledNotice" class="file-action-static terminal-disabled-notice hidden">
					此实例以 "无终端" 模式运行.
				</div>
			</div>
			<div class="card file-panel-card">
				<div class="file-panel-toolbar">
					<div class="info">
						<button id="fileSelectAllBtn" class="file-icon" type="button">${icons.instanceFileList.icon}</button>
						<span id="fileCurrentPath" class="file-current-path">./</span>
					</div>
					<div class="controls">
						<input id="fileSearchInput" class="input file-search-input" type="text" autocomplete="off" spellcheck="false" maxlength="${InputValidation.limits.fileSearch}" placeholder=" SEARCH CURRENT DIR">
						<button id="fileRuleCancelBtn" class="btn hidden" type="button">CANCEL</button>
						<button id="fileRuleActionBtn" class="btn hidden" type="button">ACTION [0]</button>
						<span class="controls-divider" aria-hidden="true">|</span>
						<button id="fileRefreshBtn" class="btn" type="button">REFRESH</button>
						<button id="fileCreateBtn" class="btn" type="button">CREATE</button>
					</div>
				</div>
				<div id="fileList" class="file-list"></div>
				<div id="filePagination" class="file-pagination hidden"></div>
			</div>
		</div>
	</section>
`);

const dom = {
	termTitle: document.getElementById('termTitle'),
	termStatus: document.getElementById('termStatus'),
	terminalDiv: document.getElementById('terminalDiv'),
	terminalWrapper: document.querySelector('#terminalSection .terminal-wrapper'),
	terminalDisabledNotice: document.getElementById('terminalDisabledNotice'),
	ctrlStart: document.getElementById('ctrlStart'),
	ctrlStop: document.getElementById('ctrlStop'),
	ctrlRestart: document.getElementById('ctrlRestart'),
	ctrlKill: document.getElementById('ctrlKill'),
	editInstanceBtn: document.getElementById('editInstanceBtn'),
	fileSelectAllBtn: document.getElementById('fileSelectAllBtn'),
	fileRuleActionBtn: document.getElementById('fileRuleActionBtn'),
	fileRuleCancelBtn: document.getElementById('fileRuleCancelBtn'),
};

const cardState = {
    reconnectTimer: null,
	resizeTimer: null,
	resizeProtectionTimer: null,
	resizeProtectionEndAt: 0,
	lastResizeCols: null,
	lastResizeRows: null,
    term: null,
    fitAddon: null,
    socket: null,
	wsDisconnectCount: 0,
	wsReconnectAttempt: 0,
	currentSvc: null,
	onEditInstance: null,
	fileSelection: null,
	onToggleSelectAllCurrentDir: null,
	isBound: false,
	ctrlCConfirming: false,
	inputChunks: [],
	inputPendingBytes: 0,
	inputDrainTimer: null,
	inputDraining: false,
	resizeHandler: null,
	resizeObserver: null,
	resizeObserverFrame: null,
};

const TERMINAL_RESIZE_PROTECTION_DURATION_MS = 1000;
const TERMINAL_RESIZE_PROTECTION_INTERVAL_MS = 100;
const TERMINAL_MAX_COLS = 4000;
const TERMINAL_MAX_ROWS = 2500;
const TERMINAL_INPUT_CHUNK_BYTES = 16 * 1024;
const TERMINAL_INPUT_BUFFER_HIGH_WATER = 1024 * 1024;
const TERMINAL_INPUT_PENDING_MAX_BYTES = 4 * 1024 * 1024;
const TERMINAL_INPUT_DRAIN_DELAY_MS = 16;

const getWsReconnectDelay = (attempt) => {
	const safeAttempt = Math.max(0, Number(attempt) || 0);
	const baseDelay = 400;
	const maxDelay = 5000;
	const jitterRatio = 0.2;
	const rawDelay = Math.min(maxDelay, baseDelay * (2 ** Math.max(0, safeAttempt - 1)));
	const jitterFactor = 1 + ((Math.random() * 2 - 1) * jitterRatio);
	return Math.max(baseDelay, Math.round(rawDelay * jitterFactor));
};

const terminalInputEncoder = new TextEncoder();

const decodeTerminalPayload = (payload) => {
	return String(payload || '');
};

const encodeTerminalControlFrame = (type, payload) => `:${type}: ${JSON.stringify(payload)}`;

const parseTerminalControlFrame = (frame) => {
	if (typeof frame !== 'string' || !frame.startsWith(':')) {
		throw new Error('无效的 WebSocket 终端帧前缀');
	}

	const separator = frame.indexOf(':', 1);
	if (separator <= 1) {
		throw new Error('无效的 WebSocket 终端帧头');
	}

	const type = frame.slice(1, separator);
	if (type.trim() !== type || /[\s:]/u.test(type)) {
		throw new Error(`无效的 WebSocket 终端帧类型: ${type}`);
	}

	const payloadText = frame.slice(separator + 1);
	if (!payloadText.startsWith(' ')) {
		throw new Error('缺少 WebSocket 终端帧负载分隔符');
	}

	return {
		type,
		payload: JSON.parse(payloadText.slice(1).trim()),
	};
};

const writeTerminalBinary = (data) => {
	if (!cardState.term || !data) {
		return;
	}
	if (data instanceof ArrayBuffer) {
		cardState.term.write(new Uint8Array(data));
		return;
	}
	if (ArrayBuffer.isView(data)) {
		cardState.term.write(new Uint8Array(data.buffer, data.byteOffset, data.byteLength));
		return;
	}
	if (data instanceof Blob) {
		data.arrayBuffer().then((buffer) => {
			if (cardState.term) {
				cardState.term.write(new Uint8Array(buffer));
			}
		}).catch((error) => {
			console.error('[WebSocket] 读取二进制终端消息失败:', error);
		});
	}
};

const sendSocketMessage = (type, payload) => {
	if (!cardState.socket || cardState.socket.readyState !== WebSocket.OPEN) {
		return false;
	}
	cardState.socket.send(JSON.stringify({ type, ...payload }));
	return true;
};

const parseTerminalControlMessage = (message) => {
	if (typeof message !== 'string') {
		throw new Error('无效的 WebSocket 终端控制消息');
	}
	if (message.startsWith(':')) {
		return parseTerminalControlFrame(message);
	}
	const payload = JSON.parse(message);
	return {
		type: String(payload.type || ''),
		payload,
	};
};

const drainSocketInput = () => {
	if (!cardState.socket || cardState.socket.readyState !== WebSocket.OPEN) {
		cardState.inputChunks.length = 0;
		cardState.inputPendingBytes = 0;
		cardState.inputDraining = false;
		return false;
	}
	while (cardState.inputChunks.length > 0) {
		if (cardState.socket.bufferedAmount > TERMINAL_INPUT_BUFFER_HIGH_WATER) {
			cardState.inputDrainTimer = setTimeout(drainSocketInput, TERMINAL_INPUT_DRAIN_DELAY_MS);
			return true;
		}
		const chunk = cardState.inputChunks.shift();
		cardState.inputPendingBytes = Math.max(0, cardState.inputPendingBytes - chunk.byteLength);
		cardState.socket.send(chunk);
	}
	cardState.inputDraining = false;
	cardState.inputDrainTimer = null;
	return true;
};

const enqueueSocketInput = (data) => {
	if (cardState.inputPendingBytes + data.byteLength > TERMINAL_INPUT_PENDING_MAX_BYTES) {
		console.error('[WebSocket] 终端输入待发送数据过多, 已断开连接.');
		closeTerminalSocket();
		return false;
	}
	for (let offset = 0; offset < data.length; offset += TERMINAL_INPUT_CHUNK_BYTES) {
		const chunk = data.slice(offset, offset + TERMINAL_INPUT_CHUNK_BYTES);
		cardState.inputChunks.push(chunk);
		cardState.inputPendingBytes += chunk.byteLength;
	}
	if (!cardState.inputDraining) {
		cardState.inputDraining = true;
		drainSocketInput();
	}
	return true;
};

const sendSocketInput = (text) => {
	if (!cardState.socket || cardState.socket.readyState !== WebSocket.OPEN) {
		return false;
	}
	const data = terminalInputEncoder.encode(String(text || ''));
	if (data.length === 0) {
		return true;
	}
	return enqueueSocketInput(data);
};

export const copyTextToClipboard = async (text) => {
	const value = String(text || '');
	if (!value) return;

	if (navigator?.clipboard?.writeText) {
		try {
			await navigator.clipboard.writeText(value);
			return;
		} catch (_) {
			// Fall through
		}
	}
};

const setHidden = (el, hidden) => {
    if (!el) return;
    el.classList.toggle('hidden', hidden);
};

const setFileRuleControls = (count) => {
	const n = Math.max(0, Number(count || 0));
	setHidden(dom.fileRuleActionBtn, n <= 0);
	setHidden(dom.fileRuleCancelBtn, n <= 0);
	if (dom.fileRuleActionBtn) {
		dom.fileRuleActionBtn.textContent = `ACTION [${n}]`;
	}
};

const applyFileSelectionSnapshot = (snapshot) => {
	const count = Number(snapshot?.count || 0);
	const active = snapshot?.allSelected === true;
	setFileRuleControls(count);
	dom.fileSelectAllBtn.classList.toggle('selected', active);
};

const setControlButtons = ({ showStart, showStop, showRestart, showKill }) => {
    setHidden(dom.ctrlStart, !showStart);
    setHidden(dom.ctrlStop, !showStop);
    setHidden(dom.ctrlRestart, !showRestart);
    setHidden(dom.ctrlKill, !showKill);
};

const resetTerminalMeta = () => {
	if (dom.termTitle) {
		dom.termTitle.innerText = 'INSTANCE';
	}
	updateStatusDisplay('offline');
};

const resetKillConfirm = () => {
    if (!dom.ctrlKill) return;
    dom.ctrlKill.innerText = 'KILL';
};

const updateStatusDisplay = (svc) => {
    if (!dom.termStatus) {
        return;
    }

    const status = (typeof svc === 'string') ? svc : (svc.restarting ? 'restarting' : (svc.updating ? 'updating' : (svc.running ? 'online' : 'offline-status')));

    if (status === 'offline') {
        dom.termStatus.innerText = '[OFFLINE]';
        dom.termStatus.className = 'status status-offline';
        setControlButtons({ showStart: true, showStop: false, showRestart: false, showKill: false });
    } else if (status === 'restarting') {
        dom.termStatus.innerText = '[RESTART]';
        dom.termStatus.className = 'status status-restart';
        setControlButtons({ showStart: true, showStop: true, showRestart: false, showKill: false });
    } else if (status === 'updating') {
        dom.termStatus.innerText = '[UPDATE]';
        dom.termStatus.className = 'status status-restart';
        setControlButtons({ showStart: false, showStop: true, showRestart: false, showKill: true });
    } else {
        const isRunning = status === 'online';
        dom.termStatus.innerText = isRunning ? '[RUN]' : '[STOP]';
        dom.termStatus.className = isRunning ? 'status status-online' : 'status status-offline';
        setControlButtons({
            showStart: !isRunning,
            showStop: isRunning,
            showRestart: isRunning,
            showKill: isRunning,
        });
    }

    resetKillConfirm();
};

const getActiveTerminalMode = (svc = cardState.currentSvc) => normalizeTerminalMode(svc?.active_terminal ?? svc?.terminal);

const hasActiveTerminal = (svc = cardState.currentSvc) => getActiveTerminalMode(svc) !== terminalMode.NO_TERMINAL;

const isPlainPipeMode = () => getActiveTerminalMode() === terminalMode.TERMINAL;

const getCurrentSvc = () => cardState.currentSvc || null;

const patchCurrentSvc = (patch = {}) => {
	if (!cardState.currentSvc || !patch || typeof patch !== 'object') {
		return;
	}
	Object.assign(cardState.currentSvc, patch);
};

const writeLocalInputEcho = (data) => {
	if (!cardState.term || !data || !isPlainPipeMode()) {
		return;
	}
	for (const ch of data) {
		if (ch === '\r') {
			cardState.term.write('\r\n');
			continue;
		}
		if (ch === '\u007f' || ch === '\b') {
			cardState.term.write('\b \b');
			continue;
		}
		if (ch >= ' ' || ch === '\t') {
			cardState.term.write(ch);
		}
	}
};

const scheduleResize = (afterResize = null) => {
    cardState.resizeTimer = clearTimer(cardState.resizeTimer);
    cardState.resizeTimer = setTimeout(() => {
        cardState.resizeTimer = null;
        sendResize();
        if (typeof afterResize === 'function') {
            afterResize();
        }
    }, DEFAULT_UI_REFRESH_INTERVAL_MS);
};

const clearResizeProtection = () => {
	if (cardState.resizeProtectionTimer) {
		clearInterval(cardState.resizeProtectionTimer);
	}
	cardState.resizeProtectionTimer = null;
	cardState.resizeProtectionEndAt = 0;
};

const fitActiveTerminal = () => {
	if (!cardState.term || !cardState.fitAddon || !hasActiveTerminal()) {
		return false;
	}
	cardState.fitAddon.fit();
	return true;
};

const runResizeProtectionCheck = () => {
	if (!fitActiveTerminal()) {
		clearResizeProtection();
		return;
	}
	sendResize();
	if (Date.now() >= cardState.resizeProtectionEndAt) {
		clearResizeProtection();
	}
};

const startResizeProtectionChecks = () => {
	cardState.resizeProtectionEndAt = Date.now() + TERMINAL_RESIZE_PROTECTION_DURATION_MS;
	if (cardState.resizeProtectionTimer) {
		return;
	}
	cardState.resizeProtectionTimer = setInterval(runResizeProtectionCheck, TERMINAL_RESIZE_PROTECTION_INTERVAL_MS);
};

const syncTerminalSizeAfterFit = () => {
	if (!fitActiveTerminal()) {
		return;
	}
	scheduleResize();
	startResizeProtectionChecks();
};

const resetResizeSyncState = () => {
	cardState.lastResizeCols = null;
	cardState.lastResizeRows = null;
};

const disconnectTerminalResizeObserver = () => {
	if (cardState.resizeObserverFrame) {
		cancelAnimationFrame(cardState.resizeObserverFrame);
		cardState.resizeObserverFrame = null;
	}
	if (!cardState.resizeObserver) {
		return;
	}
	cardState.resizeObserver.disconnect();
	cardState.resizeObserver = null;
};

const observeTerminalResize = () => {
	disconnectTerminalResizeObserver();
	if (typeof ResizeObserver !== 'function' || !dom.terminalWrapper) {
		return;
	}
	cardState.resizeObserver = new ResizeObserver(() => {
		if (cardState.resizeObserverFrame) {
			return;
		}
		cardState.resizeObserverFrame = requestAnimationFrame(() => {
			cardState.resizeObserverFrame = null;
			syncTerminalSizeAfterFit();
		});
	});
	cardState.resizeObserver.observe(dom.terminalWrapper);
};

const sendResize = (force = false) => {
    if (!cardState.term || !hasActiveTerminal()) return;

	const cols = Math.min(TERMINAL_MAX_COLS, Math.max(1, Math.floor(Number(cardState.term.cols) || 0)));
	const rows = Math.min(TERMINAL_MAX_ROWS, Math.max(1, Math.floor(Number(cardState.term.rows) || 0)));
    if (!force && cols === cardState.lastResizeCols && rows === cardState.lastResizeRows) {
        return;
    }

	if (cardState.socket && cardState.socket.readyState === WebSocket.OPEN) {
		cardState.lastResizeCols = cols;
		cardState.lastResizeRows = rows;
		sendSocketMessage('resize', { cols, rows });
	}
};

const closeTerminalSocket = () => {
	cardState.reconnectTimer = clearTimer(cardState.reconnectTimer);
	cardState.inputDrainTimer = clearTimer(cardState.inputDrainTimer);
	cardState.inputChunks.length = 0;
	cardState.inputPendingBytes = 0;
	cardState.inputDraining = false;
	resetResizeSyncState();
	if (!cardState.socket) return;
	cardState.socket.onopen = null;
	cardState.socket.onmessage = null;
	cardState.socket.onclose = null;
	cardState.socket.close();
	cardState.socket = null;
};

const disposeTerminalRuntime = () => {
	cardState.resizeTimer = clearTimer(cardState.resizeTimer);
	clearResizeProtection();
	resetResizeSyncState();
	if (cardState.resizeHandler) {
		window.removeEventListener('resize', cardState.resizeHandler);
		cardState.resizeHandler = null;
	}
	disconnectTerminalResizeObserver();
	if (cardState.term) {
		cardState.term.reset();
		cardState.term.dispose();
		cardState.term = null;
	}
	cardState.fitAddon = null;
};

const applyTerminalModeView = (svc, historySize) => {
	const enabled = !!svc && hasActiveTerminal(svc);
	dom.terminalWrapper.classList.toggle('hidden', !enabled);
	dom.terminalDisabledNotice.classList.toggle('hidden', enabled);
	if (!enabled) {
		closeTerminalSocket();
		disposeTerminalRuntime();
		return;
	}
	initTerminal(historySize ?? state.historySize);
	syncTerminalSizeAfterFit();
	if (!cardState.socket) {
		connectWebSocket(svc);
	}
};

const initTerminal = (historySize) => {
    if (cardState.term) {
        cardState.term.options.scrollback = Math.max(1000, Math.floor(historySize * 1024 / 50));
        return;
    }

	console.log('[控制台页] 正在初始化 xterm.js 实例...');
	const { TerminalCtor, FitAddonCtor } = getTerminalRuntime();
	const plainPipeMode = isPlainPipeMode();

	cardState.term = new TerminalCtor({
		// 普通 TERMINAL 管道只输出 LF, 需要让 xterm 转为 CRLF; PTY_TERMINAL 由伪终端处理换行.
		convertEol: plainPipeMode,
		cursorBlink: true,
		fontFamily: `"JetBrains Mono", "JetBrains Maple Mono Regular", "JetBrains Maple Mono"`,
		fontSize: 12,
		// letterSpacing: 0,
		// lineHeight: 1,
		// fontWeightBold: '500',
		scrollback: Math.max(1000, Math.floor(historySize * 1024 / 100)),
		theme: {
			"background": "#0a0a0a",
			"foreground": "#C7D0D9",
			"cursorColor": "#9EC1D6",
			"selectionBackground": "#9ec1d638",
            // "black": "#0a0a0a",
            // "blue": "#3D9BFF",
            // "cyan": "#5FA8A4",
            // "green": "#89E034",
            // "purple": "#8B7AA8",
            // "red": "#C47A6C",
            // "white": "#B7C1CB",
            // "yellow": "#C2A86A",
            // "brightBlack": "#5C6670",
            // "brightBlue": "#78B9FF",
            // "brightCyan": "#7CC8C3",
            // "brightGreen": "#89f71c",
            // "brightPurple": "#A896C7",
            // "brightRed": "#D99689",
            // "brightWhite": "#E2E8EE",
            // "brightYellow": "#D8C082",
		},
		allowTransparency: true,
		copyOnSelect: true,
		allowProposedApi: true,
		rightClickSelectsWord: true,
	});

	cardState.fitAddon = new FitAddonCtor();
	cardState.term.loadAddon(cardState.fitAddon);
	if (dom.terminalDiv) {
		cardState.term.open(dom.terminalDiv);
	}

    cardState.term.onData(data => {
		if (cardState.socket && cardState.socket.readyState === WebSocket.OPEN) {
			writeLocalInputEcho(data);
			sendSocketInput(data);
		}
    });

	cardState.resizeHandler = () => {
		syncTerminalSizeAfterFit();
	};
	window.addEventListener('resize', cardState.resizeHandler);
	observeTerminalResize();

	cardState.term.attachCustomKeyEventHandler((arg) => {
		if (arg.ctrlKey && arg.code === 'KeyC' && arg.type === 'keydown') {
			const selection = cardState.term.getSelection();
			if (selection && selection.length > 0) {
				void copyTextToClipboard(selection);
				return false;
			}

			// No selection: confirm before sending ^C to the running process.
			if (cardState.ctrlCConfirming) {
				return false;
			}
			cardState.ctrlCConfirming = true;
			showConfirm('未选中文本时, Ctrl+C 将发送中断信号, 可能终止正在运行的程序.\n是否发送 ^C 中断信号?', {
				title: 'SEND ^C',
				okText: 'SEND',
				cancelText: 'CANCEL',
				tone: 'danger',
			}).then((ok) => {
				if (ok) {
					sendSocketInput('\x03');
				}
			}).finally(() => {
				cardState.ctrlCConfirming = false;
			});
			return false;
		}
		return true;
	});
};

const applyHistorySize = (historySize) => {
	if (!cardState.term) {
		return;
	}
	cardState.term.options.scrollback = Math.max(1000, Math.floor(Number(historySize || 0) * 1024 / 50));
};

const connectWebSocket = (svc, options = {}) => {
    if (!svc) return;
    if (!hasActiveTerminal(svc)) return;
    const instanceName = svc.name;
    const resetCounters = options.resetCounters !== false;

	if (cardState.socket) {
		cardState.socket.onopen = null;
		cardState.socket.onmessage = null;
		cardState.socket.onclose = null;
		cardState.socket.close();
	}
	resetResizeSyncState();
	cardState.reconnectTimer = clearTimer(cardState.reconnectTimer);
	if (resetCounters) {
		cardState.wsDisconnectCount = 0;
		cardState.wsReconnectAttempt = 0;
	}

	console.log(`[WebSocket] 正在连接实例: ${instanceName}`);
	cardState.socket = createWebSocket(instanceName, {
		onOpen: () => {
			if (state.currentInstanceName !== instanceName) {
				return;
			}
			console.log(`[WebSocket] 已连接到 ${instanceName}`);
			cardState.wsDisconnectCount = 0;
			cardState.wsReconnectAttempt = 0;
			updateStatusDisplay(getCurrentSvc() || svc);
			sendResize(true);
		},
		onMessage: (event) => {
			if (state.currentInstanceName !== instanceName) {
				return;
			}
			if (typeof event.data !== 'string') {
				writeTerminalBinary(event.data);
				return;
			}
			try {
				const frame = parseTerminalControlMessage(event.data);
				if (frame.type === 'terminal' && cardState.term) {
					cardState.term.write(decodeTerminalPayload(frame.payload.data));
					return;
				}
				if (frame.type === 'error') {
					console.error('[WebSocket] 终端控制错误:', String(frame.payload.message || '未知错误'));
					return;
				}
				console.error('[WebSocket] 未知终端消息类型:', frame.type);
			} catch (e) {
				console.error('[WebSocket] 解析消息失败:', e);
			}
		},
		onClose: () => {
			console.log(`[WebSocket] 与 ${instanceName} 断开连接`);
			cardState.socket = null;

			if (state.currentInstanceName === instanceName && hasActiveTerminal()) {
				cardState.wsDisconnectCount += 1;
				cardState.wsReconnectAttempt += 1;
				const reconnectDelay = getWsReconnectDelay(cardState.wsReconnectAttempt);
				console.log(`[WebSocket] 将在 ${reconnectDelay}ms 后尝试重连 ${instanceName}...`);
				cardState.reconnectTimer = setTimeout(() => {
					connectWebSocket(svc, { resetCounters: false });
				}, reconnectDelay);
			}
		}
	});
};

const bindCardEvents = () => {
    if (cardState.isBound) {
        return;
    }
    cardState.isBound = true;

	// 终端区域滚动优先：当鼠标/焦点在终端内时，禁止滚轮滚动外层 mainContainer
	// 这样外层滚动条不会消失（不像 overflow-y: hidden），但滚动也不会被传递
	if (dom.terminalWrapper) {
		dom.terminalWrapper.addEventListener('wheel', (event) => {
			// 只在需要时阻断外层滚动；不能用 capture/stopPropagation，否则会拦截到 xterm 的 wheel
			if (!mainContainer) return;
			const el = mainContainer;
			if (el.scrollHeight <= el.clientHeight) return;
			// 如果内部（xterm viewport）已经处理并 preventDefault，则外层不会滚动
			if (event.defaultPrevented) return;

			// 尽量不影响终端自身滚动：
			// - 若 xterm 走原生滚动（viewport scrollTop），仅在触顶/触底继续滚动时阻断外层滚动链
			// - 若 xterm 走自定义 wheel 处理但未 preventDefault，则直接阻断外层滚动链
			const viewport = dom.terminalWrapper.querySelector('.xterm-viewport');
			if (viewport && viewport.scrollHeight > viewport.clientHeight) {
				const dy = event.deltaY;
				const atTop = viewport.scrollTop <= 0;
				const atBottom = viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 1;
				if ((dy < 0 && atTop) || (dy > 0 && atBottom)) {
					event.preventDefault();
				}
				return;
			}

			event.preventDefault();
		}, { passive: false });
	}

	if (dom.ctrlStart) {
        dom.ctrlStart.onclick = async () => {
			const name = state.currentInstanceName;
			if (!name) return;
			const result = await controlInstance(name, 'start');
	            if (result.ok) {
				patchCurrentSvc({ restarting: false });
	                updateStatusDisplay(getCurrentSvc());
	            }
        };
    }

	if (dom.ctrlStop) {
        dom.ctrlStop.onclick = async () => {
			const name = state.currentInstanceName;
			if (!name) return;
			const result = await controlInstance(name, 'stop');
	            if (result.ok) {
				patchCurrentSvc({ restarting: false });
	                updateStatusDisplay(getCurrentSvc());
	            }
        };
    }

    if (dom.ctrlRestart) {
        dom.ctrlRestart.onclick = async () => {
			const name = state.currentInstanceName;
			if (name) {
				void controlInstance(name, 'restart');
			}
        };
    }

	if (dom.ctrlKill) {
		dom.ctrlKill.onclick = async () => {
			const name = state.currentInstanceName;
			if (!name) return;

			const ok = await showConfirm('强制结束实例? 不等待实例正常退出, 不运行清理.', {
				title: 'KILL INSTANCE',
				okText: 'KILL',
				cancelText: 'CANCEL',
				tone: 'danger',
			});
			if (ok) {
				void controlInstance(name, 'kill');
			}
		};
	}

    if (dom.editInstanceBtn) {
        dom.editInstanceBtn.onclick = () => {
            if (typeof cardState.onEditInstance === 'function') {
				void cardState.onEditInstance(getCurrentSvc());
            }
        };
    }
};

const open = ({ svc, historySize }) => {
    if (!svc) return;
	cardState.currentSvc = svc;
	closeTerminalSocket();
	disposeTerminalRuntime();

    if (dom.termTitle) {
        dom.termTitle.innerText = `${svc.name}`;
	}
    updateStatusDisplay(svc);
	applyTerminalModeView(svc, historySize);
};

const clearView = () => {
	closeTerminalSocket();
	disposeTerminalRuntime();
	cardState.currentSvc = null;
	resetTerminalMeta();
};

const close = () => {
	resetKillConfirm();
	closeTerminalSocket();
	cardState.wsDisconnectCount = 0;
	cardState.wsReconnectAttempt = 0;
	disposeTerminalRuntime();
	cardState.currentSvc = null;
	dom.terminalWrapper.classList.remove('hidden');
	dom.terminalDisabledNotice.classList.add('hidden');
	resetTerminalMeta();
};

export const bootTerminalWorkspace = (options = {}) => {
    cardState.onEditInstance = options.onEditInstance || null;
	cardState.fileSelection = options.fileSelection || null;
	cardState.onToggleSelectAllCurrentDir = options.onToggleSelectAllCurrentDir || null;
    bindCardEvents();
	applyFileSelectionSnapshot(cardState.fileSelection?.getSnapshot?.() || null);
	cardState.fileSelection?.subscribe?.((snapshot) => {
		applyFileSelectionSnapshot(snapshot);
	});
	if (dom.fileSelectAllBtn) {
		dom.fileSelectAllBtn.onclick = () => {
			cardState.onToggleSelectAllCurrentDir?.();
		};
	}
	if (dom.fileRuleCancelBtn) {
		dom.fileRuleCancelBtn.onclick = () => {
			cardState.fileSelection?.clearSelection?.();
		};
	}
    return {
        open,
		close,
		clearView,
		applyHistorySize,
		setCurrentSvc: (svc) => {
			cardState.currentSvc = svc || null;
			const nextName = String(cardState.currentSvc?.name || '').trim();
			if (dom.termTitle && nextName) {
				dom.termTitle.innerText = nextName;
			}
			updateStatusDisplay(cardState.currentSvc || 'offline');
			applyTerminalModeView(cardState.currentSvc, state.historySize);
		},
    };
};
