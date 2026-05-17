
export const normalizeSingleLineText = (text) => text.replace(/[\r\n]+/g, ' ');

export const DEFAULT_UI_REFRESH_INTERVAL_MS = 150;

export const clearTimer = (timer) => {
    if (timer) {
        clearTimeout(timer);
    }
    return null;
};

export const parseOptionalNonNegativeInt = (input, label) => {
    const value = input ? input.value.trim() : '';
    if (value === '') {
        return { value: null };
    }

    const parsed = Number.parseInt(value, 10);
    if (!Number.isInteger(parsed) || parsed < 0) {
        return {
            error: `${label} 必须是大于等于 0 的整数`,
            input,
        };
    }

    return { value: parsed };
};

export const parseOptionalIntegerInRange = (input, label, min, max) => {
	const value = input ? input.value.trim() : '';
	if (value === '') {
		return { value: null, adjusted: false };
	}

	const parsed = Number.parseInt(value, 10);
	if (!Number.isInteger(parsed)) {
		return {
			error: `${label} 必须是整数`,
			input,
		};
	}
	return { value: Math.min(max, Math.max(min, parsed)), adjusted: false };
};

export const buildFilePathSegments = (path) => {
    const items = [{ label: './', path: '' }];
    if (!path) {
        return items;
    }

    const parts = path.split('/').filter(Boolean);
    let currentPath = '';
    parts.forEach(part => {
        currentPath = currentPath ? `${currentPath}/${part}` : part;
        items.push({ label: `${part}/`, path: currentPath });
    });
    return items;
};

export const formatFileSize = (size) => {
    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB'];
    let value = Number(size) || 0;
    let unitIndex = 0;

    while (value >= 1024 && unitIndex < units.length - 1) {
        value /= 1024;
        unitIndex += 1;
    }

    return `${value.toFixed(2)} ${units[unitIndex]}`;
};

export const getUrlSearchParam = (key) => {
    const params = new URLSearchParams(window.location.search);
    return params.get(key);
};

export const setUrlSearchParam = (key, value) => {
    const url = new URL(window.location);
    if (value) {
        url.searchParams.set(key, value);
    } else {
        url.searchParams.delete(key);
    }
    if (window.location.search !== url.search) {
        window.history.pushState({}, '', url);
    }
};

export const setUrlSearchParams = (updates = {}, options = {}) => {
	const url = new URL(window.location);
	Object.entries(updates).forEach(([key, value]) => {
		if (value) {
			url.searchParams.set(key, value);
		} else {
			url.searchParams.delete(key);
		}
	});
	if (window.location.search === url.search) {
		return;
	}
	const method = options.replace === true ? 'replaceState' : 'pushState';
	window.history[method]({}, '', url);
};

export const getUploadErrorText = (error) => {
    const message = String(error?.message || '上传失败').trim();
    if (message.includes('Target file already exists')) {
        return 'FILE ALREADY EXISTS';
    }
    if (message.includes('Failed to fetch')) {
        return 'NETWORK ERROR';
    }
    return message;
};

export const setActionsDisabled = (container, disabled) => {
	if (!container) {
		return;
	}
	container.classList.toggle('disabled', !!disabled);
	Array.from(container.querySelectorAll('button')).forEach((btn) => {
		btn.disabled = !!disabled;
	});
};

export const withActionsDisabled = async (container, task) => {
	if (!container || typeof task !== 'function') {
		return null;
	}
	if (container.classList.contains('disabled')) {
		return null;
	}
	setActionsDisabled(container, true);
	try {
		return await task();
	} finally {
		setActionsDisabled(container, false);
	}
};

export const openAnimatedModal = (modal, displayValue = 'flex') => {
	if (!modal) {
		return;
	}
	modal.style.display = displayValue;
	modal.classList.remove('closing');
	requestAnimationFrame(() => {
		modal.classList.add('visible');
	});
};

export const closeAnimatedModal = (modal, timer, onClosed, duration = 280) => {
	if (!modal) {
		return null;
	}
	clearTimer(timer);
	modal.classList.remove('visible');
	modal.classList.add('closing');
	return window.setTimeout(() => {
		modal.style.display = 'none';
		modal.classList.remove('closing');
		onClosed?.();
	}, duration);
};

export const syncToggleButtons = (buttons, activeValue, readValue = (button) => button?.dataset?.page) => {
	if (!buttons || typeof buttons.forEach !== 'function') {
		return;
	}
	buttons.forEach((button) => {
		button.classList.toggle('active', readValue(button) === activeValue);
	});
};
