import { authedFetch, getErrorMessage, parseJsonPayload, withApiFallback } from './core.js';

export const getLoginPow = async () => {
	try {
		const res = await fetch('/api/auth/pow', {
			method: 'GET',
			credentials: 'same-origin',
		});
		if (!res.ok) {
			const err = await res.text();
			return { ok: false, message: err || `HTTP ${res.status}` };
		}
		const payload = await res.json().catch(() => null);
		const data = payload?.data || null;
		if (!payload?.ok) {
			return { ok: false, message: payload?.message || '获取 PoW 参数失败' };
		}
		if (!data || data.enabled === false) {
			return { ok: true, enabled: false };
		}
		return {
			ok: true,
			enabled: data.enabled !== false,
			salt: String(data.salt || '').trim(),
			timestamp: Number(data.timestamp) || 0,
			k: Number(data.k) || 0,
			d: Number(data.d) || 0,
		};
	} catch (e) {
		return { ok: false, message: e?.message || String(e) };
	}
};

export const resetToken = async () => {
	return await withApiFallback(async () => {
		const res = await authedFetch('/api/auth/reset', {
			method: 'POST',
		});
		const payload = await parseJsonPayload(res);
		if (payload && payload.ok === false) {
			throw new Error(payload.message || 'reset token failed');
		}
		return { ok: true };
	}, (e) => ({ ok: false, message: getErrorMessage(e) }));
};

export const logout = async () => {
	return await withApiFallback(async () => {
		const res = await authedFetch('/api/auth/logout', {
			method: 'POST',
			skipUnauthorizedReload: true,
		});
		const payload = await parseJsonPayload(res);
		if (payload && payload.ok === false) {
			throw new Error(payload.message || 'logout failed');
		}
		return { ok: true };
	}, (e) => ({ ok: false, message: getErrorMessage(e) }));
};

export const login = async (user, pass, proofTimestamp, proofNonces) => {
	try {
		const res = await fetch('/api/auth/login', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({ user, pass, pow_timestamp: proofTimestamp, pow_nonces: proofNonces })
		});
		if (!res.ok) {
			if (res.status === 401) {
				return { ok: false, message: '用户名或密码错误' };
			}
			const err = await res.text();
			return { ok: false, message: err || `HTTP ${res.status}` };
		}
		const payload = await res.json();
		if (payload && payload.ok === false) {
			return { ok: false, message: payload.message || '登录失败' };
		}
		return { ok: true };
	} catch (e) {
		return { ok: false, message: e?.message || String(e) };
	}
};
