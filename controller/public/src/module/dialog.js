console.log('[模块] Dialog 加载中...');

const hiddenClass = 'hidden';
const DEFAULT_PROMPT_MAX_LENGTH = 4096;

const dom = {
    root: null,
    shell: null,
    title: null,
    message: null,
    inputWrap: null,
    input: null,
    btnCancel: null,
    btnOk: null,
};

const state = {
    closeTimer: null,
    active: null,
    queue: Promise.resolve(),
    lastFocus: null,
    isBound: false,
};

const ensureBuilt = () => {
    if (dom.root) {
        return;
    }

    const root = document.createElement('div');
    root.id = 'dialogRoot';
    root.style.display = 'none';
	root.innerHTML = /*html*/`
        <div class="dialog-backdrop">
            <div class="dialog-card-shell">
                <div class="dialog-card" role="dialog" aria-modal="true" aria-labelledby="dialogTitle" aria-describedby="dialogMessage">
                    <div id="dialogTitle" class="dialog-title"></div>
                    <div id="dialogMessage" class="dialog-message"></div>
                    <div class="dialog-input-wrap ${hiddenClass}">
                        <input id="dialogInput" class="dialog-input" type="text" autocomplete="off" spellcheck="false" />
                    </div>
                    <div class="dialog-actions">
                        <button id="dialogCancel" class="dialog-btn" type="button">CANCEL</button>
                        <button id="dialogOk" class="dialog-btn dialog-btn-primary" type="button">OK</button>
                    </div>
                </div>
            </div>
        </div>
    `;
    document.body.appendChild(root);

    dom.root = root;
    dom.shell = root.querySelector('.dialog-card');
    dom.title = root.querySelector('#dialogTitle');
    dom.message = root.querySelector('#dialogMessage');
    dom.inputWrap = root.querySelector('.dialog-input-wrap');
    dom.input = root.querySelector('#dialogInput');
    dom.btnCancel = root.querySelector('#dialogCancel');
    dom.btnOk = root.querySelector('#dialogOk');
};

const clearCloseTimer = () => {
    if (state.closeTimer) {
        clearTimeout(state.closeTimer);
        state.closeTimer = null;
    }
};

const setTone = (tone) => {
    if (!dom.root) return;
    dom.root.classList.toggle('tone-danger', tone === 'danger');
    dom.root.classList.toggle('tone-warning', tone === 'warning');
};

const bindEvents = () => {
    if (state.isBound) {
        return;
    }
    state.isBound = true;

    document.addEventListener('keydown', (e) => {
        if (!state.active) {
            return;
        }
        if (e.key === 'Escape') {
            e.preventDefault();
            state.active.onCancel();
            return;
        }
        if (e.key === 'Enter') {
            const isPrompt = state.active.type === 'prompt';
            const targetIsInput = isPrompt && (document.activeElement === dom.input);
            if (isPrompt && !targetIsInput) {
                return;
            }
            e.preventDefault();
            state.active.onOk();
        }
    }, true);

    dom.root.addEventListener('mousedown', (e) => {
        if (!state.active) return;
        const isBackdrop = e.target === dom.root || (e.target instanceof Element && e.target.classList.contains('dialog-backdrop'));
        if (!isBackdrop) return;
        if (state.active.type === 'alert') {
            state.active.onOk();
        } else {
            state.active.onCancel();
        }
    });

    dom.btnCancel.addEventListener('click', () => {
        state.active?.onCancel?.();
    });
    dom.btnOk.addEventListener('click', () => {
        state.active?.onOk?.();
    });
};

const openDialog = (payload) => new Promise((resolve) => {
    ensureBuilt();
    bindEvents();
    clearCloseTimer();

    state.lastFocus = document.activeElement;
    state.active = payload;

    setTone(payload.tone);

    if (dom.title) {
        dom.title.textContent = String(payload.title || '').trim() || 'NOTICE';
    }
    if (dom.message) {
        dom.message.textContent = String(payload.message || '').trim();
    }

    const showCancel = payload.type !== 'alert';
	if (dom.btnCancel) {
		dom.btnCancel.classList.toggle(hiddenClass, !showCancel);
		dom.btnCancel.textContent = payload.cancelText || 'CANCEL';
	}
    if (dom.btnOk) {
        dom.btnOk.textContent = payload.okText || 'OK';
    }
	if (dom.inputWrap) {
		dom.inputWrap.classList.toggle(hiddenClass, payload.type !== 'prompt');
	}
    if (dom.input) {
		const maxLength = Number.isFinite(Number(payload.maxLength)) ? Math.max(0, Math.trunc(Number(payload.maxLength))) : DEFAULT_PROMPT_MAX_LENGTH;
		dom.input.maxLength = payload.type === 'prompt' ? maxLength : DEFAULT_PROMPT_MAX_LENGTH;
		dom.input.value = payload.type === 'prompt' ? Array.from(String(payload.defaultValue || '')).slice(0, maxLength).join('') : '';
        dom.input.placeholder = payload.type === 'prompt' ? String(payload.placeholder || '') : '';
    }

    dom.root.style.display = '';
    dom.root.classList.remove('visible');
    requestAnimationFrame(() => {
        dom.root.classList.add('visible');
        if (payload.type === 'prompt' && dom.input) {
            dom.input.focus();
            dom.input.select();
        } else {
            dom.btnOk.focus();
        }
    });

    const close = (result) => {
        if (!dom.root) {
            resolve(result);
            return;
        }
        dom.root.classList.remove('visible');
        state.closeTimer = setTimeout(() => {
            dom.root.style.display = 'none';
            dom.root.classList.remove('tone-danger', 'tone-warning');
            state.closeTimer = null;
            state.active = null;
            const focus = state.lastFocus;
            state.lastFocus = null;
            try {
                focus?.focus?.();
            } catch (_) {}
            resolve(result);
        }, 260);
    };

    payload.onOk = () => {
        if (payload.type === 'confirm') {
            close(true);
            return;
        }
		if (payload.type === 'prompt') {
			const maxLength = Number.isFinite(Number(payload.maxLength)) ? Math.max(0, Math.trunc(Number(payload.maxLength))) : DEFAULT_PROMPT_MAX_LENGTH;
			const value = Array.from(String(dom.input.value || '')).slice(0, maxLength).join('').trim();
			close(value);
			return;
        }
        close(undefined);
    };
    payload.onCancel = () => {
        if (payload.type === 'confirm') {
            close(false);
            return;
        }
        if (payload.type === 'prompt') {
            close(null);
            return;
        }
        close(undefined);
    };
});

const enqueue = (payload) => {
    state.queue = state.queue
        .catch(() => null)
        .then(() => openDialog(payload));
    return state.queue;
};

export const showAlert = (message, options = {}) => {
    const payload = {
        type: 'alert',
        title: options.title || 'NOTICE',
        message,
        okText: options.okText || 'OK',
        tone: options.tone || 'warning',
        onOk: null,
        onCancel: null,
    };
    return enqueue(payload);
};

export const showConfirm = (message, options = {}) => {
    const payload = {
        type: 'confirm',
        title: options.title || 'CONFIRM',
        message,
        okText: options.okText || 'OK',
        cancelText: options.cancelText || 'CANCEL',
        tone: options.tone || 'warning',
        onOk: null,
        onCancel: null,
    };
    return enqueue(payload);
};
