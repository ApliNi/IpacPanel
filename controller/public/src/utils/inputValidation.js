const limits = Object.freeze({
	groupName: 32,
	instanceName: 32,
	instancePath: 4096,
	instanceCommand: 4096,
	instanceStopCommand: 4096,
	instanceCleanupCommand: 4096,
	instanceAccessLinksText: 2048,
	fileSearch: 4096,
	fileName: 255,
	fileContent: 10 * 1024 * 1024,
	tasksPerInstance: 512,
	taskName: 32,
	taskExpr: 128,
	taskCommand: 4096,
	taskTimezone: 128,
	trustedProxyIpsCount: 512,
	trustedProxyIp: 128,
});

const settingsLimits = Object.freeze({
	webTitle: limits.instanceName,
	listen: limits.instancePath,
	webPrivateKeyPath: limits.instancePath,
	webPublicKeyPath: limits.instancePath,
	instanceUpdateStagingDir: limits.instancePath,
	taskTimezone: limits.taskTimezone,
	trustedProxyIpsCount: limits.trustedProxyIpsCount,
	trustedProxyIp: limits.trustedProxyIp,
});

const SETTINGS_NAME_INVALID_PATTERN = /[\\/:*?"<>|]/;
const SETTINGS_SINGLE_LINE_CONTROL_CHAR_PATTERN = /[\u0000-\u001F]/;
const SETTINGS_MULTILINE_CONTROL_CHAR_PATTERN = /[\t\u0000-\u0008\u000B\u000C\u000E-\u001F]/;
const TASK_ACTIONS = Object.freeze(new Set(['start', 'stop', 'restart', 'strict_restart', 'command']));

const getTextLength = (value) => Array.from(String(value || '').trim()).length;

const ok = () => ({ ok: true, field: '', message: '' });

const reject = (field, message) => ({ ok: false, field, message });

const validateMaxLength = (field, label, value, maxLength) => {
	if (getTextLength(value) <= maxLength) {
		return ok();
	}
	return reject(field, `${label} 最多包含 ${maxLength} 个字符`);
};

const truncateText = (value, maxLength) => Array.from(String(value || '')).slice(0, maxLength).join('');

const truncateUTF8Bytes = (value, maxBytes) => {
	const text = String(value || '');
	const limit = Math.max(0, Number(maxBytes) || 0);
	if (limit <= 0) return '';
	if (typeof TextEncoder === 'undefined') {
		return truncateText(text, limit);
	}
	const encoder = new TextEncoder();
	let used = 0;
	let out = '';
	for (const char of Array.from(text)) {
		const size = encoder.encode(char).length;
		if (used + size > limit) break;
		used += size;
		out += char;
	}
	return out;
};

const normalizeTrustedProxyIps = (value) => {
	const items = Array.isArray(value) ? value : String(value || '').split(/\r?\n/);
	const seen = new Set();
	const result = [];
	items.map((item) => String(item || '').trim()).filter(Boolean).forEach((item) => {
		if (getTextLength(item) > limits.trustedProxyIp) return;
		if (seen.has(item)) return;
		seen.add(item);
		result.push(item);
	});
	return result.slice(0, limits.trustedProxyIpsCount);
};

const validateSettingsGeneralTextFields = ({ webTitle = '', listen = '', instanceUpdateStagingDir = '', taskTimezone = '', trustedProxyIpsText = '' } = {}) => {
	if (SETTINGS_NAME_INVALID_PATTERN.test(webTitle)) {
		return reject('webTitle', 'WEB TITLE 包含非法字符');
	}
	if (SETTINGS_SINGLE_LINE_CONTROL_CHAR_PATTERN.test(webTitle)) {
		return reject('webTitle', 'WEB TITLE 包含非法控制字符');
	}
	const timezoneLengthResult = validateMaxLength('taskTimezone', 'TASK TIMEZONE', taskTimezone, settingsLimits.taskTimezone);
	if (!timezoneLengthResult.ok) {
		return timezoneLengthResult;
	}
	if (SETTINGS_SINGLE_LINE_CONTROL_CHAR_PATTERN.test(taskTimezone)) {
		return reject('taskTimezone', 'TASK TIMEZONE 包含非法控制字符');
	}

	if (SETTINGS_MULTILINE_CONTROL_CHAR_PATTERN.test(trustedProxyIpsText)) {
		return reject('trustedProxyIps', 'TRUSTED PROXY IPS 包含非法控制字符');
	}
	return ok();
};

const validateSettingsWebTextFields = ({ webPrivateKeyPath = '', webPublicKeyPath = '' } = {}) => {
	if (SETTINGS_SINGLE_LINE_CONTROL_CHAR_PATTERN.test(webPrivateKeyPath)) {
		return reject('webPrivateKeyPath', 'HTTPS PRIVATE KEY PATH 包含非法控制字符');
	}
	if (SETTINGS_SINGLE_LINE_CONTROL_CHAR_PATTERN.test(webPublicKeyPath)) {
		return reject('webPublicKeyPath', 'HTTPS PUBLIC KEY PATH 包含非法控制字符');
	}
	return ok();
};

const validateSettingsTextFields = (fields = {}) => {
	const generalResult = validateSettingsGeneralTextFields(fields);
	if (!generalResult.ok) return generalResult;
	return validateSettingsWebTextFields(fields);
};

const validateInstanceTasks = (tasks) => {
	const seen = new Set();
	for (const t of tasks) {
		if (!t.name) {
			return '计划任务名称不能为空';
		}
		if (seen.has(t.name)) {
			return `计划任务名称重复: ${t.name}`;
		}
		seen.add(t.name);
		if (!t.expr) {
			return `计划任务表达式不能为空: ${t.name}`;
		}
		if (!t.action) {
			return `计划任务动作不能为空: ${t.name}`;
		}
		if (!TASK_ACTIONS.has(t.action)) {
			return `计划任务动作无效: ${t.name}`;
		}
		if (t.action === 'command' && !t.command) {
			return `发送命令不能为空: ${t.name}`;
		}
	}
	return null;
};

const validateInstanceFields = ({ name = '', group = '', path = '', command = '', terminal = 0, stopCommand = '', cleanupCommand = '' } = {}) => {
	if (!name) {
		return '名称不能为空';
	}
	if (Number(terminal) === 1 && !String(command || '').trim()) {
		return '无终端模式必须填写启动命令';
	}
	return null;
};

const formatAccessLinksText = (accessLinks) => {
	return String(accessLinks || '');
};

const parseProtocolAccessLinkLine = (line) => {
	const matched = String(line || '').trim().match(/^([a-z][a-z0-9+.-]*):\/\/.+$/i);
	if (!matched) {
		return null;
	}
	return { name: matched[1].toLowerCase(), value: matched[0] };
};

const validateAccessLinkEntry = (entry) => {
	const name = String(entry?.name || '').trim();
	const value = String(entry?.value || '').trim();
	if (!name || !value) return { entry: null, error: null };
	return { entry: { name, value }, error: null };
};

const parseAccessLinkLine = (line) => {
	const trimmedLine = String(line || '').trim();
	if (!trimmedLine) {
		return { entry: null, error: null };
	}
	if (/^#/.test(trimmedLine)) {
		return { entry: null, error: null };
	}
	const protocolEntry = parseProtocolAccessLinkLine(trimmedLine);
	if (protocolEntry) {
		return validateAccessLinkEntry(protocolEntry);
	}
	const colonIndex = trimmedLine.indexOf(':');
	if (colonIndex <= 0) {
		return { entry: null, error: null };
	}
	return validateAccessLinkEntry({
		name: trimmedLine.slice(0, colonIndex),
		value: trimmedLine.slice(colonIndex + 1),
	});
};

const parseAccessLinksText = (text) => {
	const rawText = truncateText(String(text || '').trim(), limits.instanceAccessLinksText);
	const lines = rawText.split(/\r?\n/);
	const entries = [];
	for (let index = 0; index < lines.length; index += 1) {
		const parsed = parseAccessLinkLine(lines[index]);
		if (parsed.error) {
			return { accessLinks: rawText, entries: [], error: parsed.error };
		}
		if (parsed.entry) {
			entries.push(parsed.entry);
		}
	}
	return { accessLinks: rawText, entries, error: null };
};

export const InputValidation = Object.freeze({
	limits,
	getTextLength,
	truncateText,
	truncateUTF8Bytes,
	settings: Object.freeze({
		limits: settingsLimits,
		validateGeneralTextFields: validateSettingsGeneralTextFields,
		validateWebTextFields: validateSettingsWebTextFields,
		validateTextFields: validateSettingsTextFields,
	}),
	instance: Object.freeze({
		validateFields: validateInstanceFields,
		validateTasks: validateInstanceTasks,
		parseAccessLinksText,
		formatAccessLinksText,
	}),
	normalizeTrustedProxyIps,
});
