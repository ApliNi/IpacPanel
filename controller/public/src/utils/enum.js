
export const userPerm = {
	ADMIN: 7,
	USER: 2,
	DISABLE: 0,
};

Object.keys(userPerm).forEach((key) => {
	userPerm[userPerm[key]] = key;
});

export const formatUserPerm = (perm) => {
	const p = Number(perm);
	const key = userPerm[p];
	if (typeof key === 'string' && key) {
		return `[${p}] ${key}`;
	}
	return `[${Number.isFinite(p) ? p : String(perm)}] PERM`;
};

export const userPermOptions = [userPerm.ADMIN, userPerm.USER, userPerm.DISABLE];

export const terminalMode = {
	NO_TERMINAL: 1,
	TERMINAL: 2,
	PTY_TERMINAL: 3,
};

export const normalizeTerminalMode = (mode) => {
	const value = Number(mode);
	if (value === terminalMode.NO_TERMINAL || value === terminalMode.TERMINAL || value === terminalMode.PTY_TERMINAL) {
		return value;
	}
	return terminalMode.TERMINAL;
};
