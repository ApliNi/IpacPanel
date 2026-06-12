export const applyTabPageState = (page, pairs, options = {}) => {
	const activeClass = String(options.activeClass || 'active');
	const onBeforeToggle = typeof options.onBeforeToggle === 'function' ? options.onBeforeToggle : null;
	for (const pair of pairs) {
		const name = String(pair.name || '');
		const active = name === page && (typeof pair.enabled !== 'function' || pair.enabled());
		if (onBeforeToggle) {
			onBeforeToggle(pair, active);
		}
		if (!pair.tab) {
			throw new Error(`页卡按钮缺失: ${name}`);
		}
		if (!pair.page) {
			throw new Error(`页卡页面缺失: ${name}`);
		}
		pair.tab.classList.toggle(activeClass, active);
		pair.page.classList.toggle(activeClass, active);
	}
};

export const bindTabPageButtons = (pairs, onSwitch) => {
	if (typeof onSwitch !== 'function') {
		throw new Error('页卡切换处理函数不可用');
	}
	for (const pair of pairs) {
		if (!pair.tab) {
			throw new Error(`页卡按钮缺失: ${String(pair.name || '')}`);
		}
		pair.tab.addEventListener('click', () => onSwitch(String(pair.name || '')));
	}
};
