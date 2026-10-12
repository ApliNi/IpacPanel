import { authedFetch, createHttpError, getErrorMessage, parseJsonData, withApiFallback } from './core.js';

export const getLoginPow = async () => {
	try {
		const res = await fetch('/api/auth/pow', {
			method: 'GET',
			credentials: 'same-origin',
		});
		const data = await parseJsonData(res);
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
		await parseJsonData(res);
		return { ok: true };
	}, (e) => ({ ok: false, message: getErrorMessage(e) }));
};

export const logout = async () => {
	return await withApiFallback(async () => {
		const res = await authedFetch('/api/auth/logout', {
			method: 'POST',
			suppressUnauthorizedEvent: true,
		});
		await parseJsonData(res);
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
			const err = await createHttpError(res);
			return { ok: false, message: err.message || `HTTP ${res.status}` };
		}
		await parseJsonData(res);
		return { ok: true };
	} catch (e) {
		return { ok: false, message: e?.message || String(e) };
	}
};
