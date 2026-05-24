const AUTO_RESIZE_CLEANUP = Symbol('autoResizeTextareaCleanup');
const AUTO_RESIZE_SCHEDULE = Symbol('autoResizeTextareaSchedule');

const isTextarea = (textarea) => typeof HTMLTextAreaElement === 'function' && textarea instanceof HTMLTextAreaElement;

const parsePixelValue = (value) => {
	const parsed = Number.parseFloat(value);
	return Number.isFinite(parsed) ? parsed : 0;
};

const getMaxHeight = (style) => {
	const maxHeight = Number.parseFloat(style.maxHeight);
	return Number.isFinite(maxHeight) ? maxHeight : Number.POSITIVE_INFINITY;
};

export const resizeAutoTextarea = (textarea) => {
	if (!isTextarea(textarea)) {
		return;
	}
	const style = window.getComputedStyle(textarea);
	const borderTop = parsePixelValue(style.borderTopWidth);
	const borderBottom = parsePixelValue(style.borderBottomWidth);
	const paddingTop = parsePixelValue(style.paddingTop);
	const paddingBottom = parsePixelValue(style.paddingBottom);
	const maxHeight = getMaxHeight(style);

	textarea.style.height = 'auto';
	const scrollHeight = textarea.scrollHeight;
	const targetHeight = style.boxSizing === 'border-box'
		? scrollHeight + borderTop + borderBottom
		: Math.max(0, scrollHeight - paddingTop - paddingBottom);
	const appliedHeight = Math.min(targetHeight, maxHeight);
	textarea.style.height = `${appliedHeight}px`;
	textarea.style.overflowY = targetHeight > maxHeight ? 'auto' : 'hidden';
};

export const setupAutoResizeTextarea = (textarea) => {
	if (!isTextarea(textarea)) {
		return () => {};
	}
	if (typeof textarea[AUTO_RESIZE_SCHEDULE] === 'function') {
		return textarea[AUTO_RESIZE_SCHEDULE];
	}

	let resizeFrame = 0;
	const scheduleResize = () => {
		if (resizeFrame) {
			return;
		}
		resizeFrame = requestAnimationFrame(() => {
			resizeFrame = 0;
			resizeAutoTextarea(textarea);
		});
	};
	const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(scheduleResize) : null;
	textarea.classList.add('auto-textarea');
	textarea.addEventListener('input', scheduleResize);
	if (observer) {
		observer.observe(textarea);
	}
	if (document.fonts && document.fonts.ready) {
		document.fonts.ready.then(scheduleResize).catch((error) => {
			console.error('自动调整文本框高度时等待字体加载失败:', error);
		});
	}
	scheduleResize();

	const cleanup = () => {
		textarea.removeEventListener('input', scheduleResize);
		if (observer) {
			observer.disconnect();
		}
		if (resizeFrame) {
			cancelAnimationFrame(resizeFrame);
			resizeFrame = 0;
		}
		delete textarea[AUTO_RESIZE_CLEANUP];
		delete textarea[AUTO_RESIZE_SCHEDULE];
	};
	textarea[AUTO_RESIZE_CLEANUP] = cleanup;
	textarea[AUTO_RESIZE_SCHEDULE] = scheduleResize;
	scheduleResize.cleanup = cleanup;
	return scheduleResize;
};
