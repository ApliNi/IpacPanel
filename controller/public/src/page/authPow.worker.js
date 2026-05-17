const powEncoder = new TextEncoder();

const sha256RoundConstants = [
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
];

const rightRotate = (value, bits) => (value >>> bits) | (value << (32 - bits));

const normalizeUser = (value) => {
	const user = String(value || '').trim();
	if (!user || /\s/.test(user)) {
		return '';
	}
	return user;
};

const toHex = (buffer) => Array.from(new Uint8Array(buffer)).map((x) => x.toString(16).padStart(2, '0')).join('');

const sha256FallbackHex = (text) => {
	const bytes = powEncoder.encode(String(text));
	const bitLength = bytes.length * 8;
	const paddedLength = Math.ceil((bytes.length + 9) / 64) * 64;
	const padded = new Uint8Array(paddedLength);
	padded.set(bytes);
	padded[bytes.length] = 0x80;
	const view = new DataView(padded.buffer);
	const high = Math.floor(bitLength / 0x100000000);
	const low = bitLength >>> 0;
	view.setUint32(paddedLength - 8, high, false);
	view.setUint32(paddedLength - 4, low, false);

	let h0 = 0x6a09e667;
	let h1 = 0xbb67ae85;
	let h2 = 0x3c6ef372;
	let h3 = 0xa54ff53a;
	let h4 = 0x510e527f;
	let h5 = 0x9b05688c;
	let h6 = 0x1f83d9ab;
	let h7 = 0x5be0cd19;
	const w = new Uint32Array(64);

	for (let offset = 0; offset < paddedLength; offset += 64) {
		for (let i = 0; i < 16; i++) {
			w[i] = view.getUint32(offset + i * 4, false);
		}
		for (let i = 16; i < 64; i++) {
			const s0 = rightRotate(w[i - 15], 7) ^ rightRotate(w[i - 15], 18) ^ (w[i - 15] >>> 3);
			const s1 = rightRotate(w[i - 2], 17) ^ rightRotate(w[i - 2], 19) ^ (w[i - 2] >>> 10);
			w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
		}

		let a = h0;
		let b = h1;
		let c = h2;
		let d = h3;
		let e = h4;
		let f = h5;
		let g = h6;
		let h = h7;

		for (let i = 0; i < 64; i++) {
			const s1 = rightRotate(e, 6) ^ rightRotate(e, 11) ^ rightRotate(e, 25);
			const ch = (e & f) ^ (~e & g);
			const temp1 = (h + s1 + ch + sha256RoundConstants[i] + w[i]) >>> 0;
			const s0 = rightRotate(a, 2) ^ rightRotate(a, 13) ^ rightRotate(a, 22);
			const maj = (a & b) ^ (a & c) ^ (b & c);
			const temp2 = (s0 + maj) >>> 0;

			h = g;
			g = f;
			f = e;
			e = (d + temp1) >>> 0;
			d = c;
			c = b;
			b = a;
			a = (temp1 + temp2) >>> 0;
		}

		h0 = (h0 + a) >>> 0;
		h1 = (h1 + b) >>> 0;
		h2 = (h2 + c) >>> 0;
		h3 = (h3 + d) >>> 0;
		h4 = (h4 + e) >>> 0;
		h5 = (h5 + f) >>> 0;
		h6 = (h6 + g) >>> 0;
		h7 = (h7 + h) >>> 0;
	}

	return [h0, h1, h2, h3, h4, h5, h6, h7].map((value) => value.toString(16).padStart(8, '0')).join('');
};

const sha256Hex = async (text) => {
	if (!globalThis.crypto?.subtle?.digest) {
		return sha256FallbackHex(text);
	}
	const hash = await crypto.subtle.digest('SHA-256', powEncoder.encode(String(text)));
	return toHex(hash);
};

const buildPowSeed = async (salt, user, pass, timestamp) => {
	const powSalt = String(salt || '').trim();
	if (!powSalt) {
		const error = new Error('INVALID_POW_SALT');
		error.code = 'INVALID_POW_SALT';
		throw error;
	}
	return await sha256Hex(`${powSalt}\n${normalizeUser(user)}\n${pass}\n${timestamp}`);
};

const computeLoginPow = async (user, pass, pow) => {
	const k = Number(pow?.k) || 0;
	const d = Number(pow?.d) || 0;
	const timestamp = Number(pow?.timestamp) || 0;
	const salt = String(pow?.salt || '').trim();
	if (!salt || k <= 0 || d <= 0 || timestamp <= 0) {
		const error = new Error('INVALID_POW_PARAMS');
		error.code = 'INVALID_POW_PARAMS';
		throw error;
	}

	const seed = await buildPowSeed(salt, user, pass, timestamp);
	const prefix = '4'.repeat(d);
	const nonces = [];
	let rounds = 0;

	for (let i = 0; i < k; i++) {
		let nonce = 0;
		while (true) {
			const hex = await sha256Hex(`${seed}-${i}-${nonce}`);
			if (hex.startsWith(prefix)) {
				nonces.push(nonce);
				self.postMessage({ type: 'progress', current: i + 1, total: k });
				break;
			}
			nonce += 1;
			rounds += 1;
			if (rounds % 1000 === 0) {
				self.postMessage({ type: 'progress', current: i + 1, total: k });
			}
		}
	}

	return {
		timestamp,
		nonces,
	};
};

self.onmessage = async (event) => {
	const { type, payload } = event?.data || {};
	if (type !== 'compute') {
		return;
	}
	try {
		const result = await computeLoginPow(payload?.user, payload?.pass, payload?.pow);
		self.postMessage({ type: 'result', result });
	} catch (error) {
		self.postMessage({
			type: 'error',
			code: error?.code || error?.message || 'POW_COMPUTE_FAILED',
		});
	}
};
