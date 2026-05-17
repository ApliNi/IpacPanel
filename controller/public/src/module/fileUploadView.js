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
	const barEl = progressEl?.querySelector('span');

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
