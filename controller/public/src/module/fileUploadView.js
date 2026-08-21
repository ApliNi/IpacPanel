import { formatFileSize } from '../utils/utils.js';

const clamp = (value, min, max) => Math.max(min, Math.min(max, value));

export const ceilTo = (value, decimals) => {
	const factor = Math.pow(10, Math.max(0, decimals || 0));
	return Math.ceil(clamp((Number(value) || 0), 0, 100) * factor) / factor;
};

export const renderFileUploadSummaryText = ({ success = 0, total = 0, failed = 0 }) => {
	const safeSuccess = Math.max(0, success || 0);
	const safeTotal = Math.max(0, total || 0);
	const safeFailed = Math.max(0, failed || 0);
	const finishedCount = safeSuccess + safeFailed;
	if (safeTotal <= 0) {
		return '';
	}
	let summaryText = `PROGRESS: ${safeSuccess}/${safeTotal}`;
	if (safeSuccess === safeTotal) {
		summaryText += ' DONE';
	} else if (finishedCount === safeTotal && safeFailed > 0) {
		summaryText += ' FAILED';
	}
	return summaryText;
};

export const buildFileUploadItemNode = (item) => {
	const percentBar = ceilTo(item.progress || 0, 5);
	const hasError = !!item.errorMessage;
	const row = document.createElement('div');
	row.className = 'file-upload-item';
	row.dataset.id = String(item?.id || '');

	const head = document.createElement('div');
	head.className = 'file-upload-item-head';

	const name = document.createElement('span');
	name.className = 'file-upload-item-name';
	name.textContent = String(item?.name || '');

	const removeButton = document.createElement('button');
	removeButton.className = 'file-upload-remove modal-close';
	removeButton.type = 'button';
	removeButton.dataset.id = String(item?.id || '');
	removeButton.textContent = '×';
	head.append(name, removeButton);

	const meta = document.createElement('div');
	meta.className = 'file-upload-item-meta';

	const loaded = document.createElement('span');
	loaded.className = 'file-upload-item-loaded';
	loaded.textContent = `${formatFileSize(item.loaded || 0)} / ${formatFileSize(item.size || 0)}`;

	const percent = document.createElement('span');
	percent.className = 'file-upload-item-percent';
	percent.textContent = `${percentBar.toFixed(2)}%`;
	meta.append(loaded, percent);

	const error = document.createElement('div');
	error.className = `file-upload-error${hasError ? '' : ' hidden'}`;
	error.textContent = String(item?.errorMessage || '');

	const progress = document.createElement('div');
	progress.className = `file-upload-progress${hasError ? ' hidden' : ''}`;
	const bar = document.createElement('span');
	bar.style.width = `${percentBar.toFixed(5)}%`;
	progress.appendChild(bar);

	row.append(head, meta, error, progress);
	return row;
};

export const applyFileUploadItemView = (row, item) => {
	if (!row || !item) return;
	const loadedEl = row.querySelector('.file-upload-item-loaded');
	const percentEl = row.querySelector('.file-upload-item-percent');
	const errEl = row.querySelector('.file-upload-error');
	const progressEl = row.querySelector('.file-upload-progress');
	const barEl = progressEl.querySelector('span');

	if (loadedEl) {
		loadedEl.textContent = `${formatFileSize(item.loaded || 0)} / ${formatFileSize(item.size || 0)}`;
	}

	const percentBar = ceilTo(item.progress || 0, 5);
	if (percentEl) {
		percentEl.textContent = `${percentBar.toFixed(2)}%`;
	}

	const hasError = !!item.errorMessage;
	if (errEl) {
		errEl.textContent = item.errorMessage || '';
		errEl.classList.toggle('hidden', !hasError);
	}
	if (progressEl) {
		progressEl.classList.toggle('hidden', hasError);
	}
	if (barEl) {
		barEl.style.width = `${percentBar.toFixed(5)}%`;
	}
};

/**
 * 计算文件夹组的聚合数据
 * @param {{ children: Array }} group
 * @returns {{ totalLoaded: number, totalSize: number, doneCount: number, totalCount: number, failedCount: number, progress: number }}
 */
export const computeFolderGroupAggregates = (group) => {
	const children = group?.children || [];
	let totalLoaded = 0;
	let totalSize = 0;
	let doneCount = 0;
	let failedCount = 0;
	for (const child of children) {
		totalLoaded += child.loaded || 0;
		totalSize += child.size || 0;
		if (child.status === 'DONE') doneCount += 1;
		if (child.status === 'FAILED') failedCount += 1;
	}
	const progress = totalSize > 0 ? (totalLoaded / totalSize) * 100 : 0;
	return { totalLoaded, totalSize, doneCount, totalCount: children.length, failedCount, progress };
};

/**
 * 构建文件夹组 DOM（折叠状态）
 * @param {{ id: string, name: string, expanded: boolean, children: Array }} group
 * @returns {HTMLDivElement}
 */
export const buildFileUploadFolderGroupNode = (group) => {
	const isScanning = group.scanState === 'scanning';
	const aggregates = isScanning ? null : computeFolderGroupAggregates(group);
	const container = document.createElement('div');
	container.className = 'file-upload-folder-group';
	container.dataset.id = String(group?.id || '');
	if (group.expanded) {
		container.classList.add('expanded');
	}
	if (isScanning) {
		container.classList.add('scanning');
	}

	const header = document.createElement('div');
	header.className = 'file-upload-folder-header';
	header.dataset.folderId = String(group?.id || '');

	const name = document.createElement('span');
	name.className = 'file-upload-folder-name';
	name.textContent = String(group?.name || '');

	const removeButton = document.createElement('button');
	removeButton.className = 'file-upload-remove modal-close';
	removeButton.type = 'button';
	removeButton.dataset.id = String(group?.id || '');
	removeButton.textContent = '×';

	const meta = document.createElement('div');
	meta.className = 'file-upload-item-meta';

	const loaded = document.createElement('span');
	loaded.className = 'file-upload-folder-loaded';
	if (isScanning) {
		loaded.textContent = `Scanning... ${formatFileSize(group.scannedSize || 0)} [${group.scannedCount || 0}]`;
	} else {
		loaded.textContent = `${formatFileSize(aggregates.totalLoaded)} [${aggregates.doneCount}] / ${formatFileSize(aggregates.totalSize)} [${aggregates.totalCount}]`;
	}

	const percentBar = ceilTo(isScanning ? 0 : aggregates.progress, 5);
	const percent = document.createElement('span');
	percent.className = 'file-upload-folder-percent';
	percent.textContent = `${percentBar.toFixed(2)}%`;
	meta.append(loaded, percent);

	const progress = document.createElement('div');
	progress.className = 'file-upload-progress';
	const bar = document.createElement('span');
	bar.style.width = `${percentBar.toFixed(5)}%`;
	progress.appendChild(bar);

	header.append(name, removeButton);

	const childrenContainer = document.createElement('div');
	childrenContainer.className = 'file-upload-folder-children';
	if (!group.expanded || isScanning) {
		childrenContainer.classList.add('hidden');
	}
	if (!isScanning) {
		// DONE 子项行已被 500ms 定时器从 DOM 移除，重建时不再绘制，但保留在数据中保证聚合稳定
		childrenContainer.append(...group.children.filter((c) => c.status !== 'DONE').map(buildFileUploadItemNode));
	}

	container.append(header, meta, progress, childrenContainer);
	return container;
};

/**
 * 更新文件夹组的 DOM 视图
 * @param {HTMLDivElement} row 文件夹组容器元素
 * @param {{ children: Array }} group 文件夹组数据
 */
export const applyFileUploadFolderGroupView = (row, group) => {
	if (!row || !group) return;
	const loadedEl = row.querySelector('.file-upload-folder-loaded');
	const percentEl = row.querySelector('.file-upload-folder-percent');
	const progressEl = row.querySelector('.file-upload-progress');
	const barEl = progressEl.querySelector('span');

	const isScanning = group.scanState === 'scanning';
	if (isScanning) {
		if (loadedEl) {
			loadedEl.textContent = `Scanning... ${formatFileSize(group.scannedSize || 0)} [${group.scannedCount || 0}]`;
		}
		return;
	}

	const aggregates = computeFolderGroupAggregates(group);

	if (loadedEl) {
		loadedEl.textContent = `${formatFileSize(aggregates.totalLoaded)} [${aggregates.doneCount}] / ${formatFileSize(aggregates.totalSize)} [${aggregates.totalCount}]`;
	}

	const percentBar = ceilTo(aggregates.progress, 5);
	if (percentEl) {
		percentEl.classList.remove('hidden');
		percentEl.textContent = `${percentBar.toFixed(2)}%`;
	}
	if (progressEl) {
		progressEl.classList.remove('hidden');
	}
	if (barEl) {
		barEl.style.width = `${percentBar.toFixed(5)}%`;
	}
};
