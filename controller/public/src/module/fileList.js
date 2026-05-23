import { state } from "../ui.js";
import { buildAuthedFileRawUrl } from '../api/core.js';
import { buildFilePathSegments, formatFileSize } from '../utils/utils.js';
import { getFileIconNode, getFileType } from '../utils/icon.js';
import { fetchFiles } from '../api/file.js';
import { bootFileCreateModal } from './fileCreateModal.js';
import { bootFileActionModal } from './fileActionModal.js';
import { bootFileBatchActionModal } from './fileBatchActionModal.js';
import { showAlert, showConfirm } from './dialog.js';
import { openPreview } from './mediaPreview.js';
import { InputValidation } from '../utils/inputValidation.js';

console.log('[模块] FileManager 加载中...');

const FILE_EDITOR_SOFT_LIMIT_BYTES = 10 * 1024 * 1024;

const getDirSeparator = (dirPath) => {
    if (!dirPath) return '/';
	if (dirPath.endsWith('\\')) return '\\';
	if (dirPath.endsWith('/')) return '/';
	const lastBack = dirPath.lastIndexOf('\\');
	const lastSlash = dirPath.lastIndexOf('/');
	if (lastBack > lastSlash) return '\\';
	return '/';
};

const joinDirPath = (dirPath, name, isDir) => {
    const base = String(dirPath || '');
    const fileName = String(name || '');
	if (!fileName) {
        return base;
    }
	if (!base) {
		return isDir ? `${fileName}/` : fileName;
	}
    const sep = getDirSeparator(base);
    const safeBase = base.endsWith(sep) ? base : `${base}${sep}`;
    if (!isDir) {
        return `${safeBase}${fileName}`;
    }
    return `${safeBase}${fileName}${sep}`;
};

const normalizeRelativeDirPath = (path) => {
	const value = String(path || '').replaceAll('\\', '/').replace(/^\/+/, '').replace(/\/+$/g, '');
	return value;
};

// NOTE: 这个模块会在 instanceList/terminal 两个页面都被 import。
// terminal 的文件面板 DOM 是在 module/terminal.js 中动态插入的。
// 如果 fileList.js 先于 terminal.js 执行，模块顶层直接 getElementById 会拿到 null，
// 且后续不会自动恢复，导致“有时刷新有内容、有时刷新没内容”。
const dom = {
	fileList: null,
	fileCurrentPath: null,
	fileCreateBtn: null,
	fileRefreshBtn: null,
	fileSearchInput: null,
	filePagination: null,
	filePanelCard: null,
};

const ensureDom = () => {
	if (!dom.fileList) dom.fileList = document.getElementById('fileList');
	if (!dom.fileCurrentPath) dom.fileCurrentPath = document.getElementById('fileCurrentPath');
	if (!dom.fileCreateBtn) dom.fileCreateBtn = document.getElementById('fileCreateBtn');
	if (!dom.fileRefreshBtn) dom.fileRefreshBtn = document.getElementById('fileRefreshBtn');
	if (!dom.fileSearchInput) dom.fileSearchInput = document.getElementById('fileSearchInput');
	if (!dom.filePagination) dom.filePagination = document.getElementById('filePagination');
	if (!dom.filePanelCard) dom.filePanelCard = document.querySelector('#terminalSection .file-panel-card');
	return !!(dom.fileList && dom.fileCurrentPath);
};

const LAST_DIRS_KEY = 'IpacPanel.fileLastDirs';
const LAST_DIRS_LIMIT = 128;

const safeJsonParse = (text, fallback) => {
	try {
		return JSON.parse(text);
	} catch {
		return fallback;
	}
};

const loadLastDirs = () => {
	try {
		const raw = localStorage.getItem(LAST_DIRS_KEY);
		const list = safeJsonParse(raw || '[]', []);
		return Array.isArray(list) ? list : [];
	} catch {
		return [];
	}
};

const saveLastDirs = (list) => {
	try {
		localStorage.setItem(LAST_DIRS_KEY, JSON.stringify(Array.isArray(list) ? list : []));
	} catch {
		// ignore
	}
};

const promoteLastDir = (instanceName, relDir) => {
	const name = String(instanceName || '').trim();
	const rel = String(relDir || '').replace(/\/+$/g, '').replace(/^\/+/, '');
	if (!name || !rel) {
		return;
	}
	const list = loadLastDirs().filter((r) => r && r.name !== name);
	list.unshift({ name, path: rel });
	while (list.length > LAST_DIRS_LIMIT) {
		list.pop();
	}
	saveLastDirs(list);
};

const removeLastDir = (instanceName) => {
	const name = String(instanceName || '').trim();
	if (!name) {
		return;
	}
	const list = loadLastDirs().filter((r) => r && r.name !== name);
	saveLastDirs(list);
};

const getRememberedRelDir = (instanceName) => {
	const name = String(instanceName || '').trim();
	if (!name) {
		return '';
	}
	const list = loadLastDirs();
	const item = list.find((r) => r && r.name === name);
	const rel = String(item?.path || '').replace(/\/+$/g, '').replace(/^\/+/, '');
	return rel;
};

export const renameFileLastDirsInstance = (oldInstanceName, newInstanceName) => {
	const oldName = String(oldInstanceName || '').trim();
	const newName = String(newInstanceName || '').trim();
	if (!oldName || !newName || oldName === newName) {
		return;
	}

	const rawList = loadLastDirs();
	const list = Array.isArray(rawList) ? rawList : [];
	const oldItem = list.find((r) => r && r.name === oldName);
	const oldPath = String(oldItem?.path || '').replace(/\/+$/g, '').replace(/^\/+/, '');

	let next = list.filter((r) => r && r.name !== oldName);
	if (oldPath) {
		next = next.filter((r) => r && r.name !== newName);
		next.unshift({ name: newName, path: oldPath });
	}

	while (next.length > LAST_DIRS_LIMIT) {
		next.pop();
	}
	saveLastDirs(next);
};

export const cleanupFileLastDirs = (validInstanceNames) => {
	const names = Array.isArray(validInstanceNames) ? validInstanceNames : [];
	const valid = new Set(names.map((n) => String(n || '').trim()).filter(Boolean));
	if (valid.size === 0) {
		return;
	}

	const rawList = loadLastDirs();
	const list = Array.isArray(rawList) ? rawList : [];
	const next = list.filter((r) => r && valid.has(String(r.name || '').trim()));
	if (next.length === list.length) {
		return;
	}
	saveLastDirs(next);
};

const fmState = {
    onOpenFileEditor: null,
    fileCreateModal: null,
    fileActionModal: null,
    fileBatchActionModal: null,
	fileSelection: null,
    isRuleActionBound: false,
    isBound: false,
	instanceUpdateStagingDirName: '',
	currentPage: 1,
	totalPages: 1,
	totalCount: 0,
	searchQuery: '',
	searchActivated: false,
	fileListError: '',
	currentFilePath: '',
	currentFileList: null,
	loadSeq: 0,
	selectionVersion: -1,
};

const getCurrentDir = () => String(fmState.currentFilePath || '');

const STALE_LOAD_RESULT = Object.freeze({ stale: true });

const isLoadContextStale = (context) => {
	if (!context) {
		return false;
	}
	if (context.loadSeq !== fmState.loadSeq) {
		return true;
	}
	if (context.sessionId !== state.instanceSessionSeq) {
		return true;
	}
	if (String(context.instanceName || '') !== String(state.currentInstanceName || '')) {
		return true;
	}
	return false;
};

const getUpdateDirDisplayName = (value) => {
	const raw = String(value || '').trim();
	if (!raw) {
		return '';
	}
	if (/^[A-Za-z]:[\\/]/.test(raw) || raw.startsWith('\\\\') || raw.startsWith('/')) {
		return '';
	}
	const normalized = raw.replaceAll('\\', '/').replace(/\/+$/g, '');
	if (!normalized || normalized === '.') {
		return '';
	}
	if (normalized.startsWith('./')) {
		const relative = normalized.slice(2);
		if (!relative || relative.includes('/')) {
			return '';
		}
		return relative;
	}
	if (normalized.includes('/')) {
		return '';
	}
	return normalized;
};

const getDirIconType = (name, isDir, currentDirPath) => {
	if (!isDir) {
		return '';
	}
	const currentRelDir = normalizeRelativeDirPath(currentDirPath);
	if (currentRelDir) {
		return '';
	}
	const updateDirName = String(fmState.instanceUpdateStagingDirName || '').trim();
	if (updateDirName && String(name || '').trim() === updateDirName) {
		return 'instanceUpdateDir';
	}
	return '';
};

const getFileListPage = () => Math.max(1, Number(fmState.currentPage) || 1);

const getFileListTotalPages = () => {
	const totalPages = Number(fmState.totalPages);
	if (!Number.isFinite(totalPages)) {
		return 0;
	}
	return Math.max(0, Math.trunc(totalPages));
};

const resetFileListPage = () => {
	fmState.currentPage = 1;
	fmState.totalPages = 1;
	fmState.totalCount = 0;
};

const getFileSearchQuery = () => String(fmState.searchQuery || '').trim();

const isFilePathSearchQuery = (value) => /[\\/]/.test(String(value || '').trim());

const setFileSearchQuery = (value) => {
	fmState.searchQuery = InputValidation.truncateText(value || '', InputValidation.limits.fileSearch);
};

const clearFileListError = () => {
	fmState.fileListError = '';
};

const setFileListError = (value) => {
	fmState.fileListError = String(value || '').trim();
};

const setFileSearchActivated = (value) => {
	fmState.searchActivated = value === true;
};

const clearFileSearch = () => {
	setFileSearchQuery('');
	setFileSearchActivated(false);
	if (dom.fileSearchInput) {
		dom.fileSearchInput.value = '';
	}
};

const shouldUseServerSearch = (data) => {
	if (data && Object.prototype.hasOwnProperty.call(data, 'total_pages')) {
		return Math.max(0, Number(data.total_pages) || 0) > 1;
	}
	return getFileListTotalPages() > 1;
};

const shouldRunServerSearch = () => {
	return fmState.searchActivated;
};

const getFilteredEntries = (data) => {
	const entries = Array.isArray(data?.entries) ? data.entries : [];
	const query = getFileSearchQuery().toLowerCase();
	if (!query || shouldRunServerSearch()) {
		return entries;
	}
	return entries.filter((entry) => String(entry?.name || '').toLowerCase().includes(query));
};

const getVisibleFileEntries = (data = fmState.currentFileList) => getFilteredEntries(data);

const renderFileSearchState = (data) => {
	const hasLocalSearch = !shouldRunServerSearch() && !!getFileSearchQuery();
	if (!shouldUseServerSearch(data) && !shouldRunServerSearch()) {
		setFileSearchActivated(false);
	}
	if (dom.fileSearchInput) {
		dom.fileSearchInput.classList.toggle('active', fmState.searchActivated || hasLocalSearch);
	}
	if (dom.fileSearchInput && !fmState.fileListError && dom.fileSearchInput.value !== fmState.searchQuery) {
		dom.fileSearchInput.value = fmState.searchQuery;
	}
};

const reloadFilesFromFirstPage = (path = getCurrentDir()) => {
	resetFileListPage();
	return loadFiles(path, 1);
};

const buildFilePathLink = (segment) => {
	const link = document.createElement('button');
	link.type = 'button';
	link.className = 'file-path-link';
	link.dataset.path = segment.path || '';
	link.textContent = segment.label || '';
	return link;
};

const buildFilePaginationNode = (action, enabled) => {
	const button = document.createElement('button');
	button.type = 'button';
	button.className = 'btn';
	button.dataset.pageAction = action;
	button.disabled = !enabled;
	button.textContent = action.toUpperCase();
	return button;
};

const buildFilePaginationStatus = (page, totalPages, totalCount) => {
	const status = document.createElement('span');
	status.className = 'file-pagination-status';
	const inputMax = Math.max(1, totalPages);

	const label = document.createElement('span');
	label.textContent = 'PAGE';

	const input = document.createElement('input');
	input.type = 'number';
	input.className = 'input file-pagination-input';
	input.dataset.pageInput = '1';
	input.min = '1';
	input.max = String(inputMax);
	input.step = '1';
	input.inputMode = 'numeric';
	input.autocomplete = 'off';
	input.value = String(page);
	input.setAttribute('aria-label', 'PAGE');

	const summary = document.createElement('span');
	summary.textContent = `/ ${totalPages} [${totalCount}]`;

	status.append(label, input, summary);
	return status;
};

const normalizeFilePaginationInputPage = (input) => {
	const totalPages = Math.max(1, getFileListTotalPages());
	const value = Number(input.value);
	if (!Number.isFinite(value)) {
		return getFileListPage();
	}
	return Math.min(totalPages, Math.max(1, Math.trunc(value)));
};

const submitFilePaginationInput = async (input) => {
	const nextPage = normalizeFilePaginationInputPage(input);
	input.value = String(nextPage);
	if (nextPage === getFileListPage()) {
		return;
	}
	clearFileListError();
	await loadFiles(getCurrentDir(), nextPage);
};

const buildEmptyFileListNode = (message = 'EMPTY') => {
	const empty = document.createElement('div');
	empty.className = 'file-list-empty';
	empty.textContent = String(message || '').trim() || 'EMPTY';
	return empty;
};

const buildFileRow = (entry, currentPath) => {
	const name = String(entry?.name || '');
	const isDir = !!entry?.is_dir;
	const iconType = getDirIconType(name, isDir, currentPath);
	const size = isDir ? '' : formatFileSize(entry?.size);
	const fullPath = joinDirPath(currentPath, name, isDir);
	const selected = isSelectedByRules(fullPath, isDir);
	const row = document.createElement('div');
	row.className = `file-row${selected ? ' selected' : ''}`;
	row.dataset.name = name;
	row.dataset.dir = isDir ? '1' : '0';
	row.dataset.size = String(entry?.size ?? 0);
	row.dataset.modTime = entry?.mod_time || '';

	const mainButton = document.createElement('button');
	mainButton.type = 'button';
	mainButton.className = 'file-row-main';

	const icon = document.createElement('span');
	icon.className = 'file-icon';
	const iconNode = getFileIconNode(name, isDir, iconType);
	if (iconNode) {
		icon.appendChild(iconNode);
	}

	const nameNode = document.createElement('span');
	nameNode.className = 'file-name';
	nameNode.textContent = name;

	const sizeNode = document.createElement('span');
	sizeNode.className = 'file-meta file-meta-size';
	sizeNode.textContent = size;

	const timeNode = document.createElement('span');
	timeNode.className = 'file-meta file-meta-time';
	timeNode.textContent = entry?.mod_time || '-';

	mainButton.append(icon, nameNode, sizeNode, timeNode);

	const actionButton = document.createElement('button');
	actionButton.type = 'button';
	actionButton.className = 'btn file-row-action';
	actionButton.textContent = 'ACTION';

	row.append(mainButton, actionButton);
	return row;
};

const renderFilePagination = (data) => {
	if (!dom.filePagination) {
		return;
	}
	const page = getFileListPage();
	const dataTotalPages = Number(data?.total_pages);
	const totalPages = Number.isFinite(dataTotalPages) ? Math.max(0, Math.trunc(dataTotalPages)) : getFileListTotalPages();
	const totalCount = Math.max(0, Number(fmState.totalCount) || Number(data?.total_count) || 0);
	const hasPrev = totalPages > 0 && page > 1;
	const hasNext = totalPages > 0 && page < totalPages;
	if (!data && totalCount === 0) {
		dom.filePagination.replaceChildren();
		dom.filePagination.classList.add('hidden');
		return;
	}
	dom.filePagination.classList.remove('hidden');
	const status = buildFilePaginationStatus(page, totalPages, totalCount);
	dom.filePagination.replaceChildren(buildFilePaginationNode('prev', hasPrev), status, buildFilePaginationNode('next', hasNext));
};

const getSelectionRules = () => {
	const raw = fmState.fileSelection?.getSelection?.() || {};
	return {
		include: Array.isArray(raw.include) ? raw.include : [],
		exclude: Array.isArray(raw.exclude) ? raw.exclude : [],
	};
};

const getRuleIsDir = (rule) => {
	return !!(rule?.is_dir ?? rule?.isDir);
};

const normalizePathForRule = (path, isDir) => {
	let p = String(path || '').trim();
	if (!p) return '';
	if (!isDir) return p;
	if (p.endsWith('/') || p.endsWith('\\')) return p;
	return `${p}${getDirSeparator(p)}`;
};

const normalizeRule = (rule) => {
	const rawPath = String(rule?.path || '').trim();
	if (!rawPath) return null;
	const isDir = getRuleIsDir(rule);
	const path = normalizePathForRule(rawPath, isDir);
	if (!path) return null;
	return { path, isDir, is_dir: isDir };
};

const getRuleId = (rule) => {
	const nr = normalizeRule(rule);
	if (!nr) return '';
	return `${nr.isDir ? 'd' : 'f'}:${nr.path}`;
};

const isPathWithinDirRule = (dirRulePath, path) => {
	const dir = String(dirRulePath || '');
	const p = String(path || '');
	if (!dir || !p) return false;
	return p === dir || p.startsWith(dir);
};

const getSelectionRuleCount = () => {
	const sel = getSelectionRules();
	return (sel.include.length || 0) + (sel.exclude.length || 0);
};

const cleanSelectionRules = () => {
	const dedupAndNormalize = (rules) => {
		const out = [];
		const seen = new Set();
		for (const r of Array.isArray(rules) ? rules : []) {
			const nr = normalizeRule(r);
			if (!nr) continue;
			const id = getRuleId(nr);
			if (!id || seen.has(id)) continue;
			seen.add(id);
			out.push(nr);
		}
		return out;
	};

	const current = getSelectionRules();
	let include = dedupAndNormalize(current.include);
	let exclude = dedupAndNormalize(current.exclude);

	// 1) Same exact include/exclude -> exclude wins.
	const excludeIds = new Set(exclude.map((r) => getRuleId(r)));
	include = include.filter((r) => !excludeIds.has(getRuleId(r)));

	// 2) include is covered by an exclude directory -> drop include.
	const isIncludeBlocked = (inc) => {
		const incPath = normalizePathForRule(inc.path, inc.isDir);
		for (const ex of exclude) {
			if (!ex?.path) continue;
			if (ex.isDir) {
				if (incPath.startsWith(normalizePathForRule(ex.path, true))) return true;
				continue;
			}
			if (!inc.isDir) {
				if (normalizePathForRule(ex.path, false) === normalizePathForRule(inc.path, false)) return true;
			}
		}
		return false;
	};
	include = include.filter((r) => !isIncludeBlocked(r));

	// 3) include that is already covered by another include directory -> drop it.
	// Example: include ./aa/ then include ./aa/xxx.txt -> the file rule is redundant.
	const includeDirs = include.filter((r) => r?.isDir);
	const isIncludeCoveredByDir = (inc) => {
		const incPath = normalizePathForRule(inc?.path, inc?.isDir);
		if (!incPath) return false;
		const incId = getRuleId(inc);
		for (const dir of includeDirs) {
			const dirId = getRuleId(dir);
			if (!dirId || dirId === incId) continue;
			const dirPath = normalizePathForRule(dir.path, true);
			if (!dirPath) continue;
			if (incPath.startsWith(dirPath)) return true;
		}
		return false;
	};
	include = include.filter((r) => !isIncludeCoveredByDir(r));

	// 4) exclude that does not affect any include -> drop it.
	const excludeHasEffect = (ex) => {
		if (!include.length) return false;
		const exPath = normalizePathForRule(ex.path, ex.isDir);
		if (!exPath) return false;
		if (ex.isDir) {
			for (const inc of include) {
				const incPath = normalizePathForRule(inc.path, inc.isDir);
				if (!incPath) continue;
				if (inc.isDir) {
					if (incPath.startsWith(exPath) || exPath.startsWith(incPath)) return true;
					continue;
				}
				if (incPath.startsWith(exPath)) return true;
			}
			return false;
		}
		const exFile = normalizePathForRule(ex.path, false);
		for (const inc of include) {
			const incPath = normalizePathForRule(inc.path, inc.isDir);
			if (!incPath) continue;
			if (inc.isDir) {
				if (exFile.startsWith(incPath)) return true;
				continue;
			}
			if (normalizePathForRule(inc.path, false) === exFile) return true;
		}
		return false;
	};
	exclude = exclude.filter((r) => excludeHasEffect(r));

	const prevInclude = JSON.stringify(current.include || []);
	const prevExclude = JSON.stringify(current.exclude || []);
	const nextInclude = JSON.stringify(include);
	const nextExclude = JSON.stringify(exclude);
	if (prevInclude === nextInclude && prevExclude === nextExclude) {
		return false;
	}
	fmState.fileSelection?.setSelection?.({ include, exclude });
	return true;
};

const syncFileSelectionState = () => {
	cleanSelectionRules();
	const count = getSelectionRuleCount();
	const allSelected = getCurrentDirAllSelected();
	fmState.fileSelection?.setUiState?.({ count, allSelected });
};

const getCurrentDirAllSelected = () => {
	const data = fmState.currentFileList;
	const entries = getVisibleFileEntries(data);
	if (!entries.length) {
		return false;
	}
	const baseDir = getCurrentDir();
	return entries.every((entry) => {
		const name = String(entry?.name || '');
		if (!name) return true;
		const isDir = !!entry?.is_dir;
		const fullPath = joinDirPath(baseDir, name, isDir);
		return isSelectedByRules(fullPath, isDir);
	});
};

const isSelectedByRules = (fullPath, isDir) => {
	const sel = getSelectionRules();
	const target = normalizePathForRule(fullPath, !!isDir);
	if (!target) return false;

	// Exclude has the highest priority; directory exclude is recursive.
	for (const r of sel.exclude) {
		const nr = normalizeRule(r);
		if (!nr) continue;
		if (nr.isDir) {
			if (isPathWithinDirRule(nr.path, target)) return false;
			continue;
		}
		if (!isDir && normalizePathForRule(nr.path, false) === normalizePathForRule(target, false)) {
			return false;
		}
	}

	for (const r of sel.include) {
		const nr = normalizeRule(r);
		if (!nr) continue;
		if (!nr.isDir) {
			if (!isDir && normalizePathForRule(nr.path, false) === normalizePathForRule(target, false)) {
				return true;
			}
			continue;
		}
		if (isPathWithinDirRule(nr.path, target)) return true;
	}
	return false;
};

const toggleSelectionRule = (fullPath, isDir) => {
	const path = normalizePathForRule(fullPath, !!isDir);
	if (!path) return;

	const rule = { path, isDir: !!isDir, is_dir: !!isDir };
	const id = getRuleId(rule);
	if (!id) return;

	fmState.fileSelection?.updateSelection?.((selection) => {
		const removeById = (list) => {
			const idx = list.findIndex((r) => getRuleId(r) === id);
			if (idx < 0) return false;
			list.splice(idx, 1);
			return true;
		};

		if (removeById(selection.exclude)) {
			return selection;
		}
		if (removeById(selection.include)) {
			return selection;
		}

		if (isSelectedByRules(path, !!isDir)) {
			selection.exclude.push(rule);
			return selection;
		}

		selection.include.push(rule);
		return selection;
	});
};

const getFileRowEntry = (row) => {
	if (!row) {
		return null;
	}
	const name = row.dataset.name || '';
	if (!name) {
		return null;
	}
	const isDir = row.dataset.dir === '1';
	return {
		path: joinDirPath(getCurrentDir(), name, isDir),
		name,
		isDir,
		size: Number(row.dataset.size || '0'),
		modTime: row.dataset.modTime || '',
	};
};

const openFileActionForEntry = (entry, page = 'info') => {
	if (!entry) {
		return;
	}
	fmState.fileActionModal?.open?.({
		entry: {
			path: entry.path,
			name: entry.name,
			isDir: entry.isDir,
			size: entry.size,
			modTime: entry.modTime,
		},
		page,
	});
};

const openFileBatchAction = () => {
	fmState.fileBatchActionModal?.open?.();
};

const updateFileRowSelection = () => {
	if (!dom.fileList) return;
	const currentPath = getCurrentDir();
	const rows = dom.fileList.querySelectorAll('.file-row');
	rows.forEach(row => {
		const name = row.dataset.name;
		if (!name) return;
		const isDir = row.dataset.dir === '1';
		const fullPath = joinDirPath(currentPath, name, isDir);
		row.classList.toggle('selected', isSelectedByRules(fullPath, isDir));
	});
	syncFileSelectionState();
};

export const renderFileList = (data) => {
    if (!ensureDom()) {
        return;
    }
    if (!dom.fileList || !dom.fileCurrentPath) {
        return;
    }

	const currentPath = normalizeRelativeDirPath(data?.path || '');
    const segments = buildFilePathSegments(currentPath);
	dom.fileCurrentPath.replaceChildren(...segments.map(buildFilePathLink));
	renderFileSearchState(data);
	const visibleEntries = getVisibleFileEntries(data);
	const emptyMessage = fmState.fileListError || 'EMPTY';
	if (fmState.fileListError) {
		dom.fileList.replaceChildren(buildEmptyFileListNode(emptyMessage));
		renderFilePagination(null);
		syncFileSelectionState();
		return;
	}

	if (!data || !Array.isArray(data.entries) || visibleEntries.length === 0) {
		dom.fileList.replaceChildren(buildEmptyFileListNode(emptyMessage));
		renderFilePagination(data);
		syncFileSelectionState();
		return;
	}

	dom.fileList.replaceChildren(...visibleEntries.map((entry) => buildFileRow(entry, currentPath)));
	renderFilePagination(data);
	syncFileSelectionState();
};

export const applyFileListResponse = (data, options = {}) => {
    const skipRemember = !!options.skipRemember;
	const instanceName = String(options.instanceName || state.currentInstanceName || '').trim();
	if (!instanceName) {
		return;
	}
	fmState.currentFileList = data;
	fmState.currentFilePath = normalizeRelativeDirPath(data?.path || '');
	fmState.currentPage = Math.max(1, Number(data?.page) || 1);
	fmState.totalPages = Math.max(0, Math.trunc(Number(data?.total_pages) || 0));
	fmState.totalCount = Math.max(0, Number(data?.total_count) || 0);
	clearFileListError();

	if (!skipRemember) {
		const currentRelPath = getCurrentDir();
		if (currentRelPath) {
			promoteLastDir(instanceName, currentRelPath);
		} else {
			removeLastDir(instanceName);
		}

		if (data?.missing || data?.fallback) {
			removeLastDir(instanceName);
		}
	}
    renderFileList(data);
};

const showFileListLoadError = async (result) => {
	if (!result || result.unauthorized) {
		return;
	}
	await showAlert(`加载文件列表失败: ${result.error || '操作失败'}`, { title: 'ERROR', tone: 'danger' });
};

const tryJumpToSearchPath = async () => {
	ensureDom();
	const requestedPath = InputValidation.truncateText(dom.fileSearchInput.value || '', InputValidation.limits.fileSearch).trim();
	if (dom.fileSearchInput) dom.fileSearchInput.value = requestedPath;
	if (!requestedPath || !isFilePathSearchQuery(requestedPath)) {
		return false;
	}

	const name = String(state.currentInstanceName || '').trim();
	if (!name) {
		return true;
	}

	const sessionId = state.instanceSessionSeq;
	const loadSeq = ++fmState.loadSeq;
	const context = {
		instanceName: name,
		sessionId,
		loadSeq,
	};
	const previousPage = fmState.currentPage;
	const previousTotalPages = fmState.totalPages;
	const previousTotalCount = fmState.totalCount;
	const previousSearchQuery = fmState.searchQuery;
	const previousSearchActivated = fmState.searchActivated;

	clearFileListError();
	if (dom.fileList) {
		dom.fileList.classList.add('loading');
	}

	try {
		const result = await fetchFiles(name, requestedPath, false, 1, '', { jump: true });
		if (isLoadContextStale(context)) {
			return true;
		}
		if (!result.ok || !result.data) {
			fmState.currentPage = previousPage;
			fmState.totalPages = previousTotalPages;
			fmState.totalCount = previousTotalCount;
			fmState.searchQuery = previousSearchQuery;
			fmState.searchActivated = previousSearchActivated;
			setFileListError(result.error || '路径不存在或不允许访问');
			renderFileList(fmState.currentFileList);
			return true;
		}

		resetFileListPage();
		clearFileSearch();
		applyFileListResponse(result.data, { instanceName: name });
		return true;
	} finally {
		if (dom.fileList) {
			dom.fileList.classList.remove('loading');
		}
	}
};

const submitFileSearch = async () => {
	ensureDom();
	const inputValue = InputValidation.truncateText(dom.fileSearchInput.value || '', InputValidation.limits.fileSearch);
	if (dom.fileSearchInput) dom.fileSearchInput.value = inputValue;
	if (await tryJumpToSearchPath()) {
		return;
	}

	const previousQuery = getFileSearchQuery();
	const nextQuery = String(inputValue || '').trim();
	if (previousQuery === nextQuery && fmState.searchActivated === !!nextQuery) {
		clearFileListError();
		renderFileSearchState(fmState.currentFileList);
		return;
	}

	setFileSearchQuery(inputValue);
	setFileSearchActivated(!!getFileSearchQuery());
	clearFileListError();
	resetFileListPage();
	loadFiles(getCurrentDir(), 1);
};

const loadFiles = async (path = getCurrentDir(), pageValue = getFileListPage(), options = {}) => {
	ensureDom();
	const name = String(options.instanceName || state.currentInstanceName || '').trim();
    if (!name) {
        return null;
    }
	const sessionId = Number(options.sessionId) || state.instanceSessionSeq;
	const loadSeq = ++fmState.loadSeq;
	const context = {
		instanceName: name,
		sessionId,
		loadSeq,
	};

	const requested = String(path || '');
	const searchQuery = getFileSearchQuery();
	const useServerSearch = shouldRunServerSearch();
	if (Object.prototype.hasOwnProperty.call(options, 'instanceUpdateStagingDirName')) {
		fmState.instanceUpdateStagingDirName = String(options.instanceUpdateStagingDirName || '');
	}
	const page = Math.max(1, Number(pageValue) || 1);
	const shouldRestoreFromMemory = !requested && !getCurrentDir() && page === 1 && !fmState.currentFileList;

    if (dom.fileList) {
        dom.fileList.classList.add('loading');
    }

	try {
		if (shouldRestoreFromMemory) {
			const rel = getRememberedRelDir(name);
			if (rel) {
				const restoredResult = await fetchFiles(name, rel, true, 1, useServerSearch ? searchQuery : '');
				if (isLoadContextStale(context)) {
					return STALE_LOAD_RESULT;
				}
				if (restoredResult.ok && restoredResult.data) {
					resetFileListPage();
					applyFileListResponse(restoredResult.data, { instanceName: name });
					return restoredResult.data;
				}
			}

			const rootResult = await fetchFiles(name, '', false, 1, useServerSearch ? searchQuery : '');
			if (isLoadContextStale(context)) {
				return STALE_LOAD_RESULT;
			}
			if (rootResult.ok && rootResult.data) {
				resetFileListPage();
				applyFileListResponse(rootResult.data, { skipRemember: true, instanceName: name });
				return rootResult.data;
			}
			await showFileListLoadError(rootResult);
			return null;
		}

		const result = await fetchFiles(name, requested, true, page, useServerSearch ? searchQuery : '');
	        if (isLoadContextStale(context)) {
	            return STALE_LOAD_RESULT;
	        }
	        if (!result.ok || !result.data) {
			await showFileListLoadError(result);
	            return null;
	        }
		const data = result.data;
		if (!useServerSearch && searchQuery && shouldUseServerSearch(data)) {
			setFileSearchActivated(true);
			resetFileListPage();
			return await loadFiles(requested, 1, options);
		}

	        applyFileListResponse(data, { instanceName: name });
	        if (dom.fileList) {
	            // dom.fileList.scrollTop = 0;
	        }
        return data;
    } finally {
        if (dom.fileList) {
            dom.fileList.classList.remove('loading');
        }
    }
};


const openTextFile = async (path, name, size = 0) => {
    if (!path) {
        return;
    }
	const fileSize = Math.max(0, Number(size) || 0);
	const allowLarge = fileSize > FILE_EDITOR_SOFT_LIMIT_BYTES;
	if (allowLarge) {
		const confirmed = await showConfirm(`文件大小为 ${formatFileSize(fileSize)}, 确定打开吗?`, {
			title: 'OPEN FILE',
			okText: 'OPEN',
			cancelText: 'CANCEL',
		});
		if (!confirmed) {
			return;
		}
	}

    // Open editor immediately; editor modal will fetch content in background.
    if (typeof fmState.onOpenFileEditor === 'function') {
		fmState.onOpenFileEditor({
			path,
			name: name || '',
			size: fileSize,
			allowLarge,
		});
	}
};

const reset = () => {
	fmState.loadSeq++;
	fmState.currentFilePath = '';
	fmState.currentFileList = null;
	fmState.fileSelection?.clearSelection?.();
	resetFileListPage();
	setFileSearchQuery('');
	setFileSearchActivated(false);
	clearFileListError();
	renderFileList(null);
};

const toggleSelectAllCurrentDir = () => {
	const data = fmState.currentFileList;
	const entries = getVisibleFileEntries(data);
	if (!entries.length) {
		return;
	}
	const baseDir = getCurrentDir();
	const allSelected = getCurrentDirAllSelected();
	fmState.fileSelection?.updateSelection?.((selection) => {
		for (const entry of entries) {
			const name = String(entry?.name || '');
			if (!name) continue;
			const isDir = !!entry?.is_dir;
			const fullPath = joinDirPath(baseDir, name, isDir);
			const rule = { path: normalizePathForRule(fullPath, isDir), isDir, is_dir: isDir };
			const id = getRuleId(rule);
			if (!id) continue;
			if (allSelected) {
				selection.include = (selection.include || []).filter((r) => getRuleId(r) !== id);
				if (!(selection.exclude || []).some((r) => getRuleId(r) === id)) {
					selection.exclude.push(rule);
				}
				continue;
			}
			selection.exclude = (selection.exclude || []).filter((r) => getRuleId(r) !== id);
			if (!(selection.include || []).some((r) => getRuleId(r) === id)) {
				selection.include.push(rule);
			}
		}
		return selection;
	});
	updateFileRowSelection();
};

const buildFilePreviewUrl = (instanceName, path) => {
	return buildAuthedFileRawUrl(instanceName, path);
};

const close = () => {
    fmState.fileCreateModal?.close?.();
    fmState.fileActionModal?.close?.();
	fmState.fileBatchActionModal?.close?.();
};

const bindEvents = () => {
    if (fmState.isBound) {
        return;
    }
    fmState.isBound = true;
	ensureDom();

	if (dom.fileRefreshBtn) {
		dom.fileRefreshBtn.onclick = () => {
			clearFileListError();
			loadFiles(getCurrentDir(), getFileListPage());
		};
	}
	if (dom.fileSearchInput) {
		dom.fileSearchInput.oninput = () => {
			clearFileListError();
		};
		dom.fileSearchInput.onkeydown = (event) => {
			if (event.repeat || event.isComposing || event.keyCode === 229 || event.key !== 'Enter') {
				return;
			}
			event.preventDefault();
			submitFileSearch();
		};
		dom.fileSearchInput.onblur = () => {
			submitFileSearch();
		};
	}
	if (dom.fileCreateBtn) {
		dom.fileCreateBtn.onclick = () => fmState.fileCreateModal?.open?.();
	}

	if (!fmState.isRuleActionBound) {
		fmState.isRuleActionBound = true;
		document.addEventListener('click', (event) => {
			const btn = event.target.closest('#fileRuleActionBtn');
			if (!btn) return;
			fmState.fileBatchActionModal?.open?.();
		});
	}

	if (dom.fileCurrentPath) {
			dom.fileCurrentPath.onclick = (event) => {
			const link = event.target.closest('.file-path-link');
			if (!link) {
				return;
			}
			resetFileListPage();
			clearFileSearch();
			clearFileListError();
			loadFiles(link.dataset.path || '', 1);
		};
	}

	if (dom.filePagination) {
		dom.filePagination.onclick = async (event) => {
			const btn = event.target.closest('[data-page-action]');
			if (!btn || btn.disabled) {
				return;
			}
			const action = btn.dataset.pageAction;
			const page = getFileListPage();
			if (action === 'prev') {
				clearFileListError();
				await loadFiles(getCurrentDir(), Math.max(1, page - 1));
				return;
			}
			if (action !== 'next') {
				return;
			}
			clearFileListError();
			await loadFiles(getCurrentDir(), page + 1);
		};
		dom.filePagination.onkeydown = (event) => {
			if (event.repeat || event.isComposing || event.keyCode === 229 || event.key !== 'Enter') {
				return;
			}
			const input = event.target.closest('[data-page-input]');
			if (!input) {
				return;
			}
			event.preventDefault();
			input.blur();
		};
		dom.filePagination.onfocusout = (event) => {
			const input = event.target.closest('[data-page-input]');
			if (!input) {
				return;
			}
			submitFilePaginationInput(input);
		};
	}

	if (dom.filePanelCard) {
		dom.filePanelCard.ondragenter = (event) => {
			if (!event.dataTransfer?.types?.includes?.('Files')) {
				return;
			}
			event.preventDefault();
			dom.filePanelCard.classList.add('file-panel-dragover');
		};
		dom.filePanelCard.ondragover = (event) => {
			if (!event.dataTransfer?.types?.includes?.('Files')) {
				return;
			}
			event.preventDefault();
			dom.filePanelCard.classList.add('file-panel-dragover');
		};
		dom.filePanelCard.ondragleave = (event) => {
			if (!dom.filePanelCard.contains(event.relatedTarget)) {
				dom.filePanelCard.classList.remove('file-panel-dragover');
			}
		};
		dom.filePanelCard.ondrop = (event) => {
			if (!event.dataTransfer?.types?.includes?.('Files')) {
				return;
			}
			event.preventDefault();
			dom.filePanelCard.classList.remove('file-panel-dragover');
			void fmState.fileCreateModal?.openUploadWithDataTransfer?.(event.dataTransfer);
		};
	}

	if (dom.fileList) {
		dom.fileList.oncontextmenu = (event) => {
			const row = event.target.closest('.file-row');
			if (!row) {
				return;
			}
			event.preventDefault();
			const entry = getFileRowEntry(row);
			if (!entry) {
				return;
			}
			if (getSelectionRuleCount() > 0 && isSelectedByRules(entry.path, entry.isDir)) {
				openFileBatchAction();
				return;
			}
			openFileActionForEntry(entry, 'info');
		};

		dom.fileList.onclick = async (event) => {
			const row = event.target.closest('.file-row');
			if (!row) {
				return;
			}

			const entry = getFileRowEntry(row);
			if (!entry) {
				return;
			}
			const { name, isDir, path: fullPath } = entry;

			if (event.target.closest('.file-icon')) {
				event.preventDefault();
				event.stopPropagation();
				toggleSelectionRule(fullPath, isDir);
				row.classList.toggle('selected', isSelectedByRules(fullPath, isDir));
				syncFileSelectionState();
				return;
			}

			if (event.target.closest('.file-row-action')) {
				openFileActionForEntry(entry, 'info');
				return;
			}

			if (isDir) {
				resetFileListPage();
				clearFileSearch();
				clearFileListError();
				loadFiles(fullPath, 1);
				return;
			}

			const fileName = name;
			const fileType = getFileType(fileName, false);
			if (fileType === 'zip') {
				openFileActionForEntry(entry, 'extract');
				return;
			}
			if (fileType === 'image') {
				const url = buildFilePreviewUrl(state.currentInstanceName || '', fullPath);
				if (url) {
					openPreview(url, 'img');
				}
				return;
			}
			if (fileType === 'audio' || fileType === 'video') {
				const url = buildFilePreviewUrl(state.currentInstanceName || '', fullPath);
				if (url) {
					openPreview(url, fileType);
				}
				return;
			}
			if (fileType === 'text' || fileType === 'list' || fileType === 'code' || fileType === 'config' || fileType === 'terminal' || fileType === 'file') {
				const entrySize = entry && typeof entry.size === 'number' ? entry.size : 0;
				void openTextFile(fullPath, fileName, entrySize);
				return;
			}
			openFileActionForEntry(entry, 'info');
		};
	}

};

export const bootFileManager = (options = {}) => {
    fmState.onOpenFileEditor = options.onOpenFileEditor || null;
	fmState.fileSelection = options.fileSelection || null;
	fmState.selectionVersion = -1;
	fmState.fileCreateModal = bootFileCreateModal({
        onApplyFileList: applyFileListResponse,
		onRequestReload: reloadFilesFromFirstPage,
		getCurrentDir,
    });
    fmState.fileActionModal = bootFileActionModal({
        onApplyFileList: applyFileListResponse,
		onRequestReload: reloadFilesFromFirstPage,
		getCurrentDir,
	});
	fmState.fileBatchActionModal = bootFileBatchActionModal({
		onRequestReload: reloadFilesFromFirstPage,
		fileSelection: fmState.fileSelection,
		getCurrentDir,
	});
	fmState.fileSelection?.subscribe?.((snapshot) => {
		if (!snapshot || snapshot.selectionVersion === fmState.selectionVersion) {
			return;
		}
		fmState.selectionVersion = snapshot.selectionVersion;
		updateFileRowSelection();
	});
    bindEvents();
	return {
        loadFiles,
        reset,
		close,
		forgetLastDir: removeLastDir,
		toggleSelectAllCurrentDir,
		getCurrentDir,
    };
};

export const ensureFileManagerDom = () => ensureDom();

export { getUpdateDirDisplayName };
