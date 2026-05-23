import { dispatchAuthenticated } from '../api/core.js';
import { getLoginPow, login } from '../api/auth.js';
import { InputValidation } from '../utils/inputValidation.js';

console.log('[页面] 登录页加载中...');

const dom = {
	root: null,
	card: null,
	user: null,
	pass: null,
	submit: null,
	error: null,
};

const hiddenClass = 'hidden';

let activePowWorker = null;
let submitSeq = 0;
const normalizeUser = (value) => {
	const user = String(value || '').trim();
	if (!user || /\s/.test(user)) {
		return '';
	}
	return user;
};

const mapPowErrorMessage = (error) => {
	const code = error?.code || error?.message || '';
	if (code === 'INVALID_POW_SALT') {
		return 'PoW 盐值无效';
	}
	if (code === 'INVALID_POW_PARAMS') {
		return 'PoW 参数无效';
	}
	return 'PoW 计算失败';
};

const resetPowWorker = () => {
	try {
		activePowWorker?.terminate?.();
	} catch (_) {}
	activePowWorker = null;
};

const computeLoginPow = async (user, pass, pow, seq) => {
	resetPowWorker();
	const worker = new Worker(new URL('./authPow.worker.js', import.meta.url), { type: 'module' });
	activePowWorker = worker;
	return await new Promise((resolve, reject) => {
		let settled = false;
		const finish = (callback, value) => {
			if (settled) {
				return;
			}
			settled = true;
			if (activePowWorker === worker) {
				activePowWorker = null;
			}
			try {
				worker.terminate();
			} catch (_) {}
			callback(value);
		};
		worker.onmessage = (event) => {
			if (seq !== submitSeq) {
				finish(reject, new Error('PoW 计算被取代'));
				return;
			}
			const { type, result, code, current, total } = event?.data || {};
			if (type === 'progress') {
				setLoading(true, total > 0 ? `CALCULATING ${current}/${total}` : 'CALCULATING POW...');
				return;
			}
			if (type === 'result') {
				finish(resolve, result || { timestamp: 0, nonces: [] });
				return;
			}
			if (type === 'error') {
				const error = new Error(mapPowErrorMessage({ code }));
				error.code = code || 'POW_COMPUTE_FAILED';
				finish(reject, error);
			}
		};
		worker.onerror = () => {
			finish(reject, new Error('PoW 计算失败'));
		};
		worker.postMessage({
			type: 'compute',
			payload: { user, pass, pow },
		});
	});
};
const build = () => {
	if (dom.root) {
		return;
	}

	const root = document.createElement('div');
	root.id = 'authRoot';
	root.innerHTML = /*html*/`
		<div class="auth-backdrop">
			<div class="auth-card-shell">
				<div class="auth-card">
				<div class="auth-title">LOGIN</div>
				<div class="auth-subtitle">IpacEL Terminal Panel</div>
				<div class="auth-field">
					<label>USER</label>
					<input id="authUser" type="text" autocomplete="username" spellcheck="false" maxlength="${InputValidation.limits.instanceName}" />
				</div>
				<div class="auth-field">
					<label>PASS</label>
					<input id="authPass" type="password" autocomplete="current-password" maxlength="4096" />
				</div>
				<div class="auth-field auth-feedback-field">
					<label>AUTH</label>
					<div id="authError" class="auth-error ${hiddenClass}"></div>
				<button id="authSubmit" class="auth-submit" type="button">LOGIN</button>
				</div>
				</div>
			</div>
		</div>
	`;

	document.body.appendChild(root);
	dom.root = root;
	dom.card = root.querySelector('.auth-card-shell');
	dom.user = root.querySelector('#authUser');
	dom.pass = root.querySelector('#authPass');
	dom.submit = root.querySelector('#authSubmit');
	dom.error = root.querySelector('#authError');

	requestAnimationFrame(() => {
		root.classList.add('visible');
	});
};

const setError = (msg) => {
	if (!dom.error) {
		return;
	}
	const text = String(msg || '').trim();
	if (!text) {
		dom.error.classList.add(hiddenClass);
		dom.error.textContent = '';
		return;
	}
	dom.error.classList.remove(hiddenClass);
	dom.error.textContent = text;
	if (dom.card) {
		dom.card.classList.remove('shake');
		void dom.card.offsetWidth;
		dom.card.classList.add('shake');
		setTimeout(() => {
			dom.card.classList.remove('shake');
		}, 420);
	}
};

const setLoading = (loading, label = '') => {
	if (dom.submit) {
		dom.submit.disabled = !!loading;
		dom.submit.textContent = loading ? (label || 'LOGGING IN...') : 'LOGIN';
	}
	if (dom.user) dom.user.disabled = !!loading;
	if (dom.pass) dom.pass.disabled = !!loading;
};

const hide = () => {
	if (!dom.root) {
		return;
	}
	dom.root.classList.remove('visible');
};

const submit = async () => {
	const seq = ++submitSeq;
	resetPowWorker();
	const user = normalizeUser(InputValidation.truncateText(dom.user.value || '', InputValidation.limits.instanceName));
	const pass = InputValidation.truncateText(dom.pass.value || '', 4096);
	if (!user || !pass) {
		setError('用户名和密码不能为空');
		return;
	}

	setError('');
	setLoading(true, 'FETCHING POW...');
	const pow = await getLoginPow();
	if (seq !== submitSeq) {
		return;
	}
	if (!pow.ok) {
		setError(pow.message || '获取 PoW 参数失败');
		setLoading(false);
		return;
	}

	let proof = { timestamp: 0, nonces: [] };
	if (pow.enabled) {
		setLoading(true, 'CALCULATING POW...');
		try {
			proof = await computeLoginPow(user, pass, pow, seq);
		} catch (e) {
			if (seq !== submitSeq) {
				return;
			}
			setError(e?.message || 'PoW 计算失败');
			setLoading(false);
			return;
		}
	}

	setLoading(true, 'VERIFYING LOGIN...');
	const res = await login(user, pass, proof.timestamp, proof.nonces);
	if (seq !== submitSeq) {
		return;
	}
	if (!res.ok) {
		setError(res.message || '登录失败');
		setLoading(false);
		return;
	}
	hide();
	dispatchAuthenticated();
};

export const showAuthPage = () => {
	submitSeq += 1;
	resetPowWorker();
	build();
	if (dom.root) {
		dom.root.classList.add('visible');
	}
	setError('');
	setLoading(false);
	if (dom.user) {
		dom.user.value = '';
		dom.user.focus();
	}
	if (dom.pass) {
		dom.pass.value = '';
	}

	if (dom.submit) {
		dom.submit.onclick = submit;
	}
	if (dom.pass) {
		dom.pass.onkeydown = (e) => {
			if (e.key === 'Enter') {
				submit();
			}
		};
	}
	if (dom.user) {
		dom.user.onkeydown = (e) => {
			if (e.key === 'Enter') {
				submit();
			}
		};
	}
};
