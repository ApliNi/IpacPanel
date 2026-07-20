import { mainContainer, state } from "../ui.js";
import { DEFAULT_UI_REFRESH_INTERVAL_MS, clearTimer } from '../utils/utils.js';
import { controlInstance, createWebSocket, fetchInstance } from '../api/instance.js';
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
	instanceMissingCheckSeq: 0,
	currentSvc: null,
	onEditInstance: null,
	onInstanceMissing: null,
	fileSelection: null,
	onToggleSelectAllCurrentDir: null,
	isBound: false,
	ctrlCConfirming: false,
	terminalWriteQueue: [],
	terminalWriteRunning: false,
	terminalWriteGeneration: 0,
	terminalModeAtInit: null,
	plainInputBuffer: '',
	plainPromptVisible: false,
	plainPromptRenderedText: '',
	plainPromptRenderedCols: null,
	plainPromptResizePending: false,
	plainOutputAtLineStart: true,
	plainRemoteOutputChunks: [],
	plainRemoteOutputBytes: 0,
	plainRemoteOutputTimer: null,
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
const TERMINAL_WRITE_BATCH_LIMIT = 32;
const TERMINAL_PLAIN_REMOTE_FLUSH_DELAY_MS = 16;
const TERMINAL_PLAIN_REMOTE_BUFFER_FLUSH_BYTES = 256 * 1024;
const TERMINAL_PLAIN_INPUT_MAX_CHARS = 64 * 1024;

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

const PLAIN_TERMINAL_INPUT_PROMPT = '> ';

const getByteLength = (data) => {
	if (typeof data === 'string') {
		return terminalInputEncoder.encode(data).byteLength;
	}
	if (data instanceof ArrayBuffer) {
		return data.byteLength;
	}
	if (ArrayBuffer.isView(data)) {
		return data.byteLength;
	}
	return 0;
};

const isInstanceNotFoundResult = (result) => {
	if (!result || result.ok) {
		return false;
	}
	return String(result.error || '').trim() === '实例不存在';
};

const checkInstanceStillExists = async (instanceName) => {
	const name = String(instanceName || '').trim();
	if (!name) {
		throw new Error('缺少实例名称');
	}
	const result = await fetchInstance(name);
	if (result.ok) {
		return true;
	}
	if (isInstanceNotFoundResult(result)) {
		return false;
	}
	if (result.unauthorized) {
		return true;
	}
	console.error(`[WebSocket] 检查实例 ${name} 是否存在失败:`, result.error || '未知错误');
	return true;
};

const createTerminalWriteEntry = (data, afterWrite = null) => {
	let chunk = null;
	if (typeof data === 'string') {
		chunk = data;
	} else if (data instanceof ArrayBuffer) {
		chunk = new Uint8Array(data);
	} else if (ArrayBuffer.isView(data)) {
		chunk = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
	}
	if (!chunk) {
		return null;
	}
	return {
		chunk,
		afterWrite: typeof afterWrite === 'function' ? afterWrite : null,
	};
};

const resetTerminalWriteQueue = () => {
	cardState.terminalWriteQueue.length = 0;
	cardState.terminalWriteRunning = false;
	cardState.terminalWriteGeneration += 1;
};

const takeTerminalWriteBatch = () => {
	const first = cardState.terminalWriteQueue.shift();
	if (!first || first.afterWrite || typeof first.chunk !== 'string') {
		return first;
	}

	const parts = [first.chunk];
	while (parts.length < TERMINAL_WRITE_BATCH_LIMIT) {
		const next = cardState.terminalWriteQueue[0];
		if (!next || next.afterWrite || typeof next.chunk !== 'string') {
			break;
		}
		parts.push(cardState.terminalWriteQueue.shift().chunk);
	}
	return { chunk: parts.join(''), afterWrite: null };
};

const drainTerminalWriteQueue = () => {
	if (!cardState.term) {
		resetTerminalWriteQueue();
		return;
	}

	const entry = takeTerminalWriteBatch();
	if (!entry) {
		cardState.terminalWriteRunning = false;
		return;
	}

	cardState.terminalWriteRunning = true;
	const writeGeneration = cardState.terminalWriteGeneration;
	cardState.term.write(entry.chunk, () => {
		if (writeGeneration !== cardState.terminalWriteGeneration) {
			return;
		}
		try {
			if (entry.afterWrite) {
				entry.afterWrite();
			}
		} catch (error) {
			console.error('[终端] 写入回调执行失败:', error);
		}
		drainTerminalWriteQueue();
	});
};

const queueTerminalWrite = (data, afterWrite = null) => {
	const entry = createTerminalWriteEntry(data, afterWrite);
	if (!cardState.term || !entry) {
		return;
	}
	cardState.terminalWriteQueue.push(entry);
	if (!cardState.terminalWriteRunning) {
		drainTerminalWriteQueue();
	}
};

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
	if (isPlainPipeMode()) {
		enqueuePlainTerminalRemoteOutput(data);
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
		const writeGeneration = cardState.terminalWriteGeneration;
		data.arrayBuffer().then((buffer) => {
			if (cardState.term && writeGeneration === cardState.terminalWriteGeneration) {
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

const resetPlainTerminalInputState = () => {
	cardState.plainRemoteOutputTimer = clearTimer(cardState.plainRemoteOutputTimer);
	cardState.plainRemoteOutputChunks.length = 0;
	cardState.plainRemoteOutputBytes = 0;
	cardState.plainInputBuffer = '';
	cardState.plainPromptVisible = false;
	cardState.plainPromptRenderedText = '';
	cardState.plainPromptRenderedCols = null;
	cardState.plainPromptResizePending = false;
	cardState.plainOutputAtLineStart = true;
};

const splitGraphemes = (text) => {
	const value = String(text || '');
	if (!value) {
		return [];
	}
	if (typeof Intl !== 'undefined' && typeof Intl.Segmenter === 'function') {
		const segmenter = new Intl.Segmenter('zh-CN', { granularity: 'grapheme' });
		return Array.from(segmenter.segment(value), item => item.segment);
	}
	return Array.from(value);
};

const removeLastPlainInputGrapheme = () => {
	const chars = splitGraphemes(cardState.plainInputBuffer);
	chars.pop();
	cardState.plainInputBuffer = chars.join('');
};

const clearRenderedPlainTerminalPromptLine = () => {
	if (!cardState.term || !cardState.plainPromptVisible) {
		return;
	}
	cardState.term.write(getClearPlainTerminalPromptSequence(cardState.plainPromptRenderedText, getTerminalCols()));
};

const getPlainTerminalPromptText = () => {
	if (!cardState.plainInputBuffer) {
		return '';
	}
	return `${PLAIN_TERMINAL_INPUT_PROMPT}${cardState.plainInputBuffer}`;
};

const getTerminalCols = () => Math.min(TERMINAL_MAX_COLS, Math.max(1, Math.floor(Number(cardState.term.cols) || 0)));

const getTerminalRows = () => Math.min(TERMINAL_MAX_ROWS, Math.max(1, Math.floor(Number(cardState.term.rows) || 0)));

const getCodePointWidth = (codePoint) => {
	if (codePoint === 0) {
		return 0;
	}
	if (codePoint < 32 || (codePoint >= 0x7f && codePoint < 0xa0)) {
		return 0;
	}
	if (
		(codePoint >= 0x0300 && codePoint <= 0x036f) ||
		(codePoint >= 0x1ab0 && codePoint <= 0x1aff) ||
		(codePoint >= 0x1dc0 && codePoint <= 0x1dff) ||
		(codePoint >= 0x20d0 && codePoint <= 0x20ff) ||
		(codePoint >= 0xfe00 && codePoint <= 0xfe0f)
	) {
		return 0;
	}
	if (
		(codePoint >= 0x1100 && codePoint <= 0x115f) ||
		(codePoint >= 0x2329 && codePoint <= 0x232a) ||
		(codePoint >= 0x2e80 && codePoint <= 0xa4cf) ||
		(codePoint >= 0xac00 && codePoint <= 0xd7a3) ||
		(codePoint >= 0xf900 && codePoint <= 0xfaff) ||
		(codePoint >= 0xfe10 && codePoint <= 0xfe19) ||
		(codePoint >= 0xfe30 && codePoint <= 0xfe6f) ||
		(codePoint >= 0xff00 && codePoint <= 0xff60) ||
		(codePoint >= 0xffe0 && codePoint <= 0xffe6) ||
		(codePoint >= 0x1f300 && codePoint <= 0x1faff)
	) {
		return 2;
	}
	return 1;
};

const measurePlainTerminalColumns = (text, cols) => {
	let width = 0;
	for (const ch of String(text || '')) {
		if (ch === '\t') {
			width += 8 - (width % 8);
			continue;
		}
		width += getCodePointWidth(ch.codePointAt(0));
	}
	return Math.max(0, width);
};

const getPlainTerminalPromptRows = (text, cols) => {
	const width = measurePlainTerminalColumns(text, cols);
	return Math.max(1, Math.ceil(width / cols));
};

const getClearPlainTerminalPromptSequence = (text, renderedCols = getTerminalCols()) => {
	const cols = Math.min(TERMINAL_MAX_COLS, Math.max(1, Math.floor(Number(renderedCols) || getTerminalCols())));
	const rows = getPlainTerminalPromptRows(text, cols);
	let sequence = '\r\x1b[2K';
	for (let index = 1; index < rows; index += 1) {
		sequence += '\x1b[1A\r\x1b[2K';
	}
	return sequence;
};

const clearPlainTerminalPromptLine = () => {
	if (!cardState.term || !cardState.plainPromptVisible) {
		return false;
	}
	const currentCols = getTerminalCols();
	const renderedCols = cardState.plainPromptRenderedCols === currentCols ? cardState.plainPromptRenderedCols : currentCols;
	queueTerminalWrite(getClearPlainTerminalPromptSequence(cardState.plainPromptRenderedText, renderedCols));
	cardState.plainPromptVisible = false;
	cardState.plainPromptRenderedText = '';
	cardState.plainPromptRenderedCols = null;
	cardState.plainOutputAtLineStart = true;
	return true;
};

const renderPlainTerminalPromptLine = () => {
	if (!cardState.term || !isPlainPipeMode()) {
		return;
	}
	if (cardState.plainPromptResizePending) {
		return;
	}

	const promptText = getPlainTerminalPromptText();
	const currentCols = getTerminalCols();
	if (promptText === cardState.plainPromptRenderedText && cardState.plainPromptRenderedCols === currentCols) {
		return;
	}
	if (cardState.plainPromptVisible && cardState.plainPromptRenderedCols === currentCols && promptText.startsWith(cardState.plainPromptRenderedText)) {
		const appendedText = promptText.slice(cardState.plainPromptRenderedText.length);
		queueTerminalWrite(appendedText);
		cardState.plainPromptRenderedText = promptText;
		cardState.plainPromptRenderedCols = currentCols;
		cardState.plainOutputAtLineStart = false;
		return;
	}

	clearPlainTerminalPromptLine();
	if (!promptText) {
		return;
	}

	const linePrefix = cardState.plainOutputAtLineStart ? '' : '\r\n';
	queueTerminalWrite(`${linePrefix}\x1b[0m${promptText}`);
	cardState.plainPromptVisible = true;
	cardState.plainPromptRenderedText = promptText;
	cardState.plainPromptRenderedCols = currentCols;
	cardState.plainOutputAtLineStart = false;
};

const updatePlainTerminalOutputPosition = (data) => {
	if (!isPlainPipeMode()) {
		return;
	}
	if (data instanceof ArrayBuffer) {
		if (data.byteLength === 0) {
			return;
		}
		const view = new Uint8Array(data);
		const lastByte = view[view.byteLength - 1];
		cardState.plainOutputAtLineStart = lastByte === 10 || lastByte === 13;
		return;
	}
	if (ArrayBuffer.isView(data)) {
		if (data.byteLength === 0) {
			return;
		}
		const view = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
		const lastByte = view[view.byteLength - 1];
		cardState.plainOutputAtLineStart = lastByte === 10 || lastByte === 13;
		return;
	}
	if (typeof data !== 'string') {
		cardState.plainOutputAtLineStart = false;
		return;
	}
	if (data.length === 0) {
		return;
	}
	const lastChar = data.charAt(data.length - 1);
	cardState.plainOutputAtLineStart = lastChar === '\r' || lastChar === '\n';
};

const writeTerminalOutput = (data) => {
	if (!cardState.term || !data) {
		return;
	}
	if (isPlainPipeMode()) {
		enqueuePlainTerminalRemoteOutput(data);
		return;
	}
	cardState.term.write(data);
	updatePlainTerminalOutputPosition(data);
};

const flushPlainTerminalRemoteOutput = () => {
	cardState.plainRemoteOutputTimer = null;
	if (!cardState.term || cardState.plainRemoteOutputChunks.length === 0) {
		return;
	}

	const chunks = cardState.plainRemoteOutputChunks.splice(0);
	cardState.plainRemoteOutputBytes = 0;
	const preservePlainPrompt = isPlainPipeMode() && cardState.plainPromptVisible;
	if (preservePlainPrompt) {
		clearPlainTerminalPromptLine();
	}
	for (const chunk of chunks) {
		queueTerminalWrite(chunk);
		updatePlainTerminalOutputPosition(chunk);
	}
	if (preservePlainPrompt) {
		renderPlainTerminalPromptLine();
	}
};

const enqueuePlainTerminalRemoteOutput = (data) => {
	if (!cardState.term || !data) {
		return;
	}
	if (data instanceof Blob) {
		const writeGeneration = cardState.terminalWriteGeneration;
		data.arrayBuffer().then((buffer) => {
			if (cardState.term && writeGeneration === cardState.terminalWriteGeneration) {
				enqueuePlainTerminalRemoteOutput(buffer);
			}
		}).catch((error) => {
			console.error('[WebSocket] 读取二进制终端消息失败:', error);
		});
		return;
	}

	cardState.plainRemoteOutputChunks.push(data);
	cardState.plainRemoteOutputBytes += getByteLength(data);
	if (cardState.plainRemoteOutputBytes >= TERMINAL_PLAIN_REMOTE_BUFFER_FLUSH_BYTES) {
		cardState.plainRemoteOutputTimer = clearTimer(cardState.plainRemoteOutputTimer);
		flushPlainTerminalRemoteOutput();
		return;
	}
	if (!cardState.plainRemoteOutputTimer) {
		cardState.plainRemoteOutputTimer = setTimeout(flushPlainTerminalRemoteOutput, TERMINAL_PLAIN_REMOTE_FLUSH_DELAY_MS);
	}
};

const appendPlainTerminalInputChar = (ch) => {
	if (ch === '\u007f' || ch === '\b') {
		removeLastPlainInputGrapheme();
		return;
	}
	const codePoint = ch.codePointAt(0);
	const isControlChar = (codePoint >= 0 && codePoint < 0x20 && ch !== '\t') || (codePoint >= 0x7f && codePoint <= 0x9f);
	if (!isControlChar && cardState.plainInputBuffer.length < TERMINAL_PLAIN_INPUT_MAX_CHARS) {
		cardState.plainInputBuffer += ch;
	}
};

const isPlainTerminalInputControlSequence = (text) => {
	return text.startsWith('\x1b');
};

const collectPlainTerminalCompleteLines = (data) => {
	const lines = [];
	const text = String(data || '');
	if (isPlainTerminalInputControlSequence(text)) {
		return lines;
	}
	for (let index = 0; index < text.length; index += 1) {
		const ch = text.charAt(index);
		if (ch === '\r' || ch === '\n') {
			if (ch === '\r' && text.charAt(index + 1) === '\n') {
				index += 1;
			}
			lines.push(`${cardState.plainInputBuffer}\n`);
			cardState.plainInputBuffer = '';
			continue;
		}
		appendPlainTerminalInputChar(ch);
	}
	return lines;
};

const handlePlainTerminalInput = (data) => {
	if (!cardState.socket || cardState.socket.readyState !== WebSocket.OPEN) {
		return;
	}
	const completeLines = collectPlainTerminalCompleteLines(data);
	for (const line of completeLines) {
		sendSocketInput(line);
	}
	renderPlainTerminalPromptLine();
};

const handleTerminalInput = (data) => {
	if (!cardState.socket || cardState.socket.readyState !== WebSocket.OPEN) {
		return;
	}
	if (isPlainPipeMode()) {
		handlePlainTerminalInput(data);
		return;
	}
	sendSocketInput(data);
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
        setControlButtons({ showStart: true, showStop: true, showRestart: false, showKill: true });
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

const fitActiveTerminal = (afterFit = null) => {
	if (!cardState.term || !cardState.fitAddon || !hasActiveTerminal()) {
		return false;
	}
	if (cardState.plainPromptResizePending) {
		return true;
	}
	const shouldRestorePlainPrompt = isPlainPipeMode()
		&& cardState.plainPromptVisible
		&& !!cardState.plainInputBuffer
		&& !cardState.plainPromptResizePending;
	if (!shouldRestorePlainPrompt) {
		cardState.fitAddon.fit();
		if (typeof afterFit === 'function') {
			afterFit();
		}
		return true;
	}

	cardState.plainPromptResizePending = true;
	const clearSequence = getClearPlainTerminalPromptSequence(
		cardState.plainPromptRenderedText,
		cardState.plainPromptRenderedCols || getTerminalCols(),
	);
	cardState.plainPromptVisible = false;
	cardState.plainPromptRenderedText = '';
	cardState.plainPromptRenderedCols = null;
	cardState.plainOutputAtLineStart = true;
	queueTerminalWrite(clearSequence, () => {
		if (!cardState.term || !cardState.fitAddon || !hasActiveTerminal()) {
			cardState.plainPromptResizePending = false;
			return;
		}
		cardState.fitAddon.fit();
		cardState.plainPromptResizePending = false;
		renderPlainTerminalPromptLine();
		if (typeof afterFit === 'function') {
			afterFit();
		}
	});
	return true;
};

const runResizeProtectionCheck = () => {
	if (!fitActiveTerminal(() => sendResize())) {
		clearResizeProtection();
		return;
	}
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
	if (!fitActiveTerminal(() => scheduleResize())) {
		return;
	}
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

	const cols = getTerminalCols();
	const rows = getTerminalRows();
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
	cardState.instanceMissingCheckSeq += 1;
	cardState.inputDrainTimer = clearTimer(cardState.inputDrainTimer);
	cardState.inputChunks.length = 0;
	cardState.inputPendingBytes = 0;
	cardState.inputDraining = false;
	resetTerminalWriteQueue();
	clearRenderedPlainTerminalPromptLine();
	resetPlainTerminalInputState();
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
	resetTerminalWriteQueue();
	clearRenderedPlainTerminalPromptLine();
	resetPlainTerminalInputState();
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
	cardState.terminalModeAtInit = null;
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
    const activeTerminalMode = getActiveTerminalMode();
    if (cardState.term) {
		if (cardState.terminalModeAtInit !== activeTerminalMode) {
			disposeTerminalRuntime();
		} else {
			cardState.term.options.scrollback = Math.max(1000, Math.floor(historySize * 1024 / 50));
			return;
		}
    }

	console.log('[控制台页] 正在初始化 xterm.js 实例...');
	const { TerminalCtor, FitAddonCtor } = getTerminalRuntime();
	const plainPipeMode = activeTerminalMode === terminalMode.TERMINAL;
	cardState.terminalModeAtInit = activeTerminalMode;

	cardState.term = new TerminalCtor({
		// 普通 TERMINAL 管道只输出 LF, 需要让 xterm 转为 CRLF; PTY_TERMINAL 由伪终端处理换行.
		convertEol: plainPipeMode,
		cursorBlink: !plainPipeMode,
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

    cardState.term.onData(handleTerminalInput);

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
					writeTerminalOutput(decodeTerminalPayload(frame.payload.data));
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
			resetTerminalWriteQueue();
			clearRenderedPlainTerminalPromptLine();
			resetPlainTerminalInputState();

			if (state.currentInstanceName === instanceName && hasActiveTerminal()) {
				cardState.wsDisconnectCount += 1;
				cardState.wsReconnectAttempt += 1;
				const reconnectDelay = getWsReconnectDelay(cardState.wsReconnectAttempt);
				console.log(`[WebSocket] 将在 ${reconnectDelay}ms 后尝试重连 ${instanceName}...`);
				cardState.reconnectTimer = setTimeout(() => {
					const checkSeq = cardState.instanceMissingCheckSeq + 1;
					cardState.instanceMissingCheckSeq = checkSeq;
					checkInstanceStillExists(instanceName).then((exists) => {
						if (checkSeq !== cardState.instanceMissingCheckSeq || state.currentInstanceName !== instanceName || !hasActiveTerminal()) {
							return;
						}
						if (!exists) {
							console.warn(`[WebSocket] 实例 ${instanceName} 不存在, 返回实例列表`);
							cardState.reconnectTimer = clearTimer(cardState.reconnectTimer);
							cardState.wsDisconnectCount = 0;
							cardState.wsReconnectAttempt = 0;
							if (typeof cardState.onInstanceMissing !== 'function') {
								throw new Error('实例不存在处理器未初始化');
							}
							Promise.resolve(cardState.onInstanceMissing(instanceName)).catch((error) => {
								console.error(`[WebSocket] 处理实例 ${instanceName} 不存在失败:`, error);
							});
							return;
						}
						connectWebSocket(svc, { resetCounters: false });
					}).catch((error) => {
						console.error(`[WebSocket] 检查实例 ${instanceName} 是否存在失败:`, error);
						if (checkSeq !== cardState.instanceMissingCheckSeq || state.currentInstanceName !== instanceName || !hasActiveTerminal()) {
							return;
						}
						connectWebSocket(svc, { resetCounters: false });
					});
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
	cardState.onInstanceMissing = options.onInstanceMissing || null;
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
