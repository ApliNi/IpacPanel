import { applyWebTitle, mainModalOverlay } from '../ui.js';
import { applyControllerUpdate, completeControllerUpdateUpload, fetchControllerUpdateStatus, initControllerUpdateUpload, uploadControllerUpdateChunk } from '../api/controllerUpdate.js';
import { clearTimer, formatFileSize, getUploadErrorText, withActionsDisabled } from '../utils/utils.js';
import { fetchSettings, restartController, updateSettings } from '../api/settings.js';
import { InputValidation } from '../utils/inputValidation.js';
import { showAlert, showConfirm } from './dialog.js';

const CHUNK_SIZE = 8 * 1024 * 1024;

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="panelSettingsModal" class="modal-overlay">
		<div class="modal-card panel-settings-modal-card">
			<div class="modal-header">
				<span class="modal-title">SETTINGS</span>
				<button id="panelSettingsClose" class="modal-close" type="button">×</button>
			</div>
			<div class="modal-form panel-settings-form">
				<div class="filter-group panel-settings-main-tabs">
					<button id="panelSettingsMainTabConfig" class="filter-btn active" type="button" data-page="config">CONFIG</button>
					<button id="panelSettingsMainTabUpload" class="filter-btn" type="button" data-page="upload">UPDATE</button>
					<button id="panelSettingsMainTabDebug" class="filter-btn" type="button" data-page="debug">DEBUG</button>
				</div>
				<div id="panelSettingsConfigTabs" class="filter-group filter-group-v2">
					<button id="panelSettingsConfigTabOptions" class="filter-btn active" type="button" data-page="options">OPTIONS</button>
					<button id="panelSettingsConfigTabMetrics" class="filter-btn" type="button" data-page="metrics">METRICS</button>
					<button id="panelSettingsConfigTabWeb" class="filter-btn" type="button" data-page="web">WEB</button>
					<button id="panelSettingsConfigTabPow" class="filter-btn" type="button" data-page="pow">POW</button>
				</div>
				<form id="panelSettingsOptionsPage" class="panel-settings-page active" novalidate>
					<div class="field-group">
						<span>WEB TITLE</span>
						<input id="panelSettingsWebTitle" type="text" autocomplete="off" maxlength="${InputValidation.settings.limits.webTitle}" placeholder="IpacPanel">
					</div>
					<div class="field-group">
						<span>LISTEN</span>
						<input id="panelSettingsListen" type="text" autocomplete="off" maxlength="${InputValidation.settings.limits.listen}" placeholder=" 127.0.0.1:25555">
					</div>
					<div class="field-group">
						<span>HISTORY SIZE</span>
						<input id="panelSettingsHistorySize" type="number" min="2" max="65536" step="1" autocomplete="off" placeholder=" 27 KB">
					</div>
					<div class="field-group">
						<span>AUTO START INTERVAL</span>
						<input id="panelSettingsAutoStartInterval" type="number" min="0" max="86400000" step="1" autocomplete="off" placeholder=" 200 ms">
					</div>
					<div class="field-group">
						<span>AUTO RESTART INTERVAL</span>
						<input id="panelSettingsAutoRestartInterval" type="number" min="0" max="86400000" step="1" autocomplete="off" placeholder=" 1000 ms">
					</div>
					<div class="field-group">
						<span>INSTANCE UPDATE DIR</span>
						<input id="panelSettingsInstanceUpdateStagingDir" type="text" autocomplete="off" maxlength="${InputValidation.settings.limits.instanceUpdateStagingDir}" placeholder=" ./!InstanceUpdate/">
					</div>
					<div class="field-group">
						<span>TRUSTED PROXY IPS</span>
						<textarea id="panelSettingsTrustedProxyIps" rows="4" spellcheck="false" autocomplete="off" maxlength="${InputValidation.settings.limits.trustedProxyIpsCount * (InputValidation.settings.limits.trustedProxyIp + 1)}" placeholder=" 127.0.0.1"></textarea>
					</div>
					<div class="modal-actions modal-actions-split">
						<span id="panelSettingsStatus" aria-live="polite"></span>
						<div class="modal-actions-group">
							<button id="panelSettingsCancel" class="btn" type="button">CANCEL</button>
							<button id="panelSettingsSave" class="btn btn-start" type="submit">SAVE</button>
						</div>
					</div>
				</form>
				<form id="panelSettingsMetricsPage" class="panel-settings-page" novalidate>
					<div class="field-group field-group-dynamic-label">
						<label class="checkbox-group instance-advanced-toggle">
							<input id="panelSettingsMetricsEnabled" type="checkbox" autocomplete="off">
							<span>ENABLED METRICS</span>
						</label>
					</div>
					<div class="field-group field-group-dynamic-label">
						<label class="checkbox-group instance-advanced-toggle">
							<input id="panelSettingsMetricsPublicDashboard" type="checkbox" autocomplete="off">
							<span>PUBLIC DASHBOARD</span>
						</label>
						<div class="file-action-static instance-advanced-note">
							允许未登录用户以普通用户权限访问仪表板
						</div>
					</div>
					<div class="field-group">
						<span>STORAGE MODE</span>
						<div class="select-wrapper">
							<select id="panelSettingsMetricsStorageMode" autocomplete="off">
								<option value="memory">MEMORY</option>
								<option value="sqlite">SQLITE</option>
							</select>
						</div>
					</div>
					<div id="panelSettingsMetricsMemoryGroup" class="field-group">
						<span>MEMORY MAX MIN</span>
						<input id="panelSettingsMetricsMemoryMaxMin" type="number" min="1" max="10080" step="1" autocomplete="off" placeholder=" 30 min">
					</div>
					<div id="panelSettingsMetricsSqliteGroup" class="field-group hidden">
						<span>SQLITE MAX STORAGE DAY</span>
						<input id="panelSettingsMetricsSqliteMaxDay" type="number" min="0" max="36500" step="1" autocomplete="off" placeholder=" 7 day">
						<div class="file-action-static instance-advanced-note">
							设置为 0 将不会清理旧数据
						</div>
						<span>SQLITE COMPACT AFTER DAY</span>
						<input id="panelSettingsMetricsSqliteCompactAfterDay" type="number" min="0" max="36500" step="1" autocomplete="off" placeholder=" 2 day">
						<div class="file-action-static instance-advanced-note">
							设置为 0 将不会压缩数据
						</div>
					</div>
					<div class="modal-actions modal-actions-split">
						<span id="panelSettingsMetricsStatus" aria-live="polite"></span>
						<div class="modal-actions-group">
							<button id="panelSettingsMetricsCancel" class="btn" type="button">CANCEL</button>
							<button id="panelSettingsMetricsSave" class="btn btn-start" type="submit">SAVE</button>
						</div>
					</div>
				</form>
				<form id="panelSettingsWebPage" class="panel-settings-page" novalidate>
					<div class="field-group field-group-dynamic-label">
						<label class="checkbox-group instance-advanced-toggle">
							<input id="panelSettingsWebEnableHttps" type="checkbox" autocomplete="off">
							<span>ENABLE HTTPS</span>
						</label>
					</div>
					<div class="field-group field-group-dynamic-label">
						<label class="checkbox-group instance-advanced-toggle">
							<input id="panelSettingsWebForceHttps" type="checkbox" autocomplete="off">
							<span>FORCE HTTPS</span>
						</label>
					</div>
					<div class="field-group">
						<span>HTTPS PRIVATE KEY PATH</span>
						<input id="panelSettingsWebPrivateKeyPath" type="text" autocomplete="off" maxlength="${InputValidation.settings.limits.webPrivateKeyPath}" placeholder=" ./data/cert/key">
					</div>
					<div class="field-group">
						<span>HTTPS PUBLIC KEY PATH</span>
						<input id="panelSettingsWebPublicKeyPath" type="text" autocomplete="off" maxlength="${InputValidation.settings.limits.webPublicKeyPath}" placeholder=" ./data/cert/pem">
					</div>
					<div class="modal-actions modal-actions-split">
						<span id="panelSettingsWebStatus" aria-live="polite"></span>
						<div class="modal-actions-group">
							<button id="panelSettingsWebCancel" class="btn" type="button">CANCEL</button>
							<button id="panelSettingsWebSave" class="btn btn-start" type="submit">SAVE</button>
						</div>
					</div>
				</form>
				<form id="panelSettingsPowPage" class="panel-settings-page" novalidate>
					<div class="field-group field-group-dynamic-label">
						<label class="checkbox-group instance-advanced-toggle">
							<input id="panelSettingsPowEnabled" type="checkbox" autocomplete="off">
							<span>ENABLED POW</span>
						</label>
					</div>
					<div class="field-group">
						<span>TASK COUNT</span>
						<input id="panelSettingsPowTaskCount" type="number" min="1" max="128" step="1" autocomplete="off" placeholder=" 24">
					</div>
					<div class="field-group">
						<span>DIFFICULTY</span>
						<input id="panelSettingsPowDifficulty" type="number" min="1" max="10" step="1" autocomplete="off" placeholder=" 3">
					</div>
					<div class="field-group">
						<span>TIMESTAMP MAX SKEW</span>
						<input id="panelSettingsPowTimestampMaxSkew" type="number" min="1" max="3600" step="1" autocomplete="off" placeholder=" 90 s">
						<div class="file-action-static instance-advanced-note">
							设置过小可能导致无法登录
						</div>
					</div>
					<div class="modal-actions modal-actions-split">
						<span id="panelSettingsPowStatus" aria-live="polite"></span>
						<div class="modal-actions-group">
							<button id="panelSettingsPowCancel" class="btn" type="button">CANCEL</button>
							<button id="panelSettingsPowSave" class="btn btn-start" type="submit">SAVE</button>
						</div>
					</div>
				</form>
				<div id="panelSettingsDebugPage" class="panel-settings-page">
					<div class="field-group field-group-dynamic-label">
						<label class="checkbox-group instance-advanced-toggle">
							<input id="panelSettingsDebugEnabled" type="checkbox" autocomplete="off">
							<span>ENABLED DEBUG</span>
						</label>
						<div class="file-action-static instance-advanced-note">
							显示调试日志, 启用后可能影响性能
						</div>
					</div>
					<div class="field-group">
						<span>CONTROLLER</span>
						<button id="panelSettingsRestartController" class="btn" type="button">RESTART CONTROLLER</button>
					</div>
					<div class="modal-actions modal-actions-split">
						<span id="panelSettingsDebugStatus" aria-live="polite"></span>
						<div class="modal-actions-group">
							<button id="panelSettingsDebugCancel" class="btn" type="button">CANCEL</button>
							<button id="panelSettingsDebugSave" class="btn btn-start" type="button">SAVE</button>
						</div>
					</div>
				</div>
				<div id="panelSettingsUploadPage" class="panel-settings-page">
					<div class="controller-update-body">
						<div class="field-group">
							<button id="controllerUpdateDropzone" class="file-upload-dropzone compact" type="button">
								<span class="file-upload-title">DROP RELEASE HERE</span>
								<span class="file-upload-subtitle">Expected: IpacPanel-[platform]-[architecture].zip</span>
							</button>
							<input id="controllerUpdateFileInput" class="hidden" type="file" accept=".zip" autocomplete="off">
						</div>
						<div id="controllerUpdateProgressWrap" class="file-upload-item hidden">
							<div class="file-upload-item-head">
								<span id="controllerUpdateFileName" class="file-upload-item-name"></span>
								<span id="controllerUpdatePercent" class="file-upload-subtitle">0%</span>
							</div>
							<div class="file-upload-item-meta">
								<span id="controllerUpdateLoaded">0 B / 0 B</span>
								<span id="controllerUpdateStatus">WAITING</span>
							</div>
							<div class="file-upload-progress"><span id="controllerUpdateProgress" class="progress-fill-zero"></span></div>
							<div id="controllerUpdateError" class="file-upload-error hidden"></div>
						</div>
					</div>
					<div id="controllerUpdateActions" class="modal-actions modal-actions-split">
						<span id="controllerUpdateActionStatus" aria-live="polite"></span>
						<div class="modal-actions-group">
							<button id="controllerUpdateCancel" class="btn" type="button">CANCEL</button>
							<button id="controllerUpdateApply" class="btn btn-start" type="button">UPDATE</button>
						</div>
					</div>
				</div>
			</div>
		</div>
	</div>
`);

const dom = {
	modal: document.getElementById('panelSettingsModal'),
	close: document.getElementById('panelSettingsClose'),
	configMainTab: document.getElementById('panelSettingsMainTabConfig'),
	uploadMainTab: document.getElementById('panelSettingsMainTabUpload'),
	debugMainTab: document.getElementById('panelSettingsMainTabDebug'),
	configTabs: document.getElementById('panelSettingsConfigTabs'),
	optionsConfigTab: document.getElementById('panelSettingsConfigTabOptions'),
	metricsConfigTab: document.getElementById('panelSettingsConfigTabMetrics'),
	webConfigTab: document.getElementById('panelSettingsConfigTabWeb'),
	powConfigTab: document.getElementById('panelSettingsConfigTabPow'),
	optionsPage: document.getElementById('panelSettingsOptionsPage'),
	metricsPage: document.getElementById('panelSettingsMetricsPage'),
	webPage: document.getElementById('panelSettingsWebPage'),
	powPage: document.getElementById('panelSettingsPowPage'),
	debugPage: document.getElementById('panelSettingsDebugPage'),
	uploadPage: document.getElementById('panelSettingsUploadPage'),
	webTitle: document.getElementById('panelSettingsWebTitle'),
	listen: document.getElementById('panelSettingsListen'),
	webEnableHttps: document.getElementById('panelSettingsWebEnableHttps'),
	webForceHttps: document.getElementById('panelSettingsWebForceHttps'),
	webPrivateKeyPath: document.getElementById('panelSettingsWebPrivateKeyPath'),
	webPublicKeyPath: document.getElementById('panelSettingsWebPublicKeyPath'),
	historySize: document.getElementById('panelSettingsHistorySize'),
	autoStartInterval: document.getElementById('panelSettingsAutoStartInterval'),
	autoRestartInterval: document.getElementById('panelSettingsAutoRestartInterval'),
	instanceUpdateStagingDir: document.getElementById('panelSettingsInstanceUpdateStagingDir'),
	trustedProxyIps: document.getElementById('panelSettingsTrustedProxyIps'),
	metricsEnabled: document.getElementById('panelSettingsMetricsEnabled'),
	metricsPublicDashboard: document.getElementById('panelSettingsMetricsPublicDashboard'),
	metricsMemoryMaxMin: document.getElementById('panelSettingsMetricsMemoryMaxMin'),
	metricsMemoryGroup: document.getElementById('panelSettingsMetricsMemoryGroup'),
	metricsStorageMode: document.getElementById('panelSettingsMetricsStorageMode'),
	metricsSqliteMaxDay: document.getElementById('panelSettingsMetricsSqliteMaxDay'),
	metricsSqliteCompactAfterDay: document.getElementById('panelSettingsMetricsSqliteCompactAfterDay'),
	metricsSqliteGroup: document.getElementById('panelSettingsMetricsSqliteGroup'),
	metricsCancel: document.getElementById('panelSettingsMetricsCancel'),
	metricsStatus: document.getElementById('panelSettingsMetricsStatus'),
	metricsSave: document.getElementById('panelSettingsMetricsSave'),
	webCancel: document.getElementById('panelSettingsWebCancel'),
	webStatus: document.getElementById('panelSettingsWebStatus'),
	webSave: document.getElementById('panelSettingsWebSave'),
	powEnabled: document.getElementById('panelSettingsPowEnabled'),
	powTaskCount: document.getElementById('panelSettingsPowTaskCount'),
	powDifficulty: document.getElementById('panelSettingsPowDifficulty'),
	powTimestampMaxSkew: document.getElementById('panelSettingsPowTimestampMaxSkew'),
	powCancel: document.getElementById('panelSettingsPowCancel'),
	powStatus: document.getElementById('panelSettingsPowStatus'),
	debugEnabled: document.getElementById('panelSettingsDebugEnabled'),
	debugCancel: document.getElementById('panelSettingsDebugCancel'),
	debugSave: document.getElementById('panelSettingsDebugSave'),
	restartController: document.getElementById('panelSettingsRestartController'),
	settingsCancel: document.getElementById('panelSettingsCancel'),
	settingsSave: document.getElementById('panelSettingsSave'),
	settingsStatus: document.getElementById('panelSettingsStatus'),
	powSave: document.getElementById('panelSettingsPowSave'),
	debugStatus: document.getElementById('panelSettingsDebugStatus'),
	dropzone: document.getElementById('controllerUpdateDropzone'),
	fileInput: document.getElementById('controllerUpdateFileInput'),
	progressWrap: document.getElementById('controllerUpdateProgressWrap'),
	fileName: document.getElementById('controllerUpdateFileName'),
	percent: document.getElementById('controllerUpdatePercent'),
	loaded: document.getElementById('controllerUpdateLoaded'),
	status: document.getElementById('controllerUpdateStatus'),
	progress: document.getElementById('controllerUpdateProgress'),
	error: document.getElementById('controllerUpdateError'),
	actionStatus: document.getElementById('controllerUpdateActionStatus'),
	cancel: document.getElementById('controllerUpdateCancel'),
	apply: document.getElementById('controllerUpdateApply'),
};

const panelSettingsState = {
	closeTimer: null,
	isBound: false,
	locked: false,
	pending: false,
	selectedFile: null,
	updateFileName: '',
	updateFileSize: 0,
	currentMainPage: 'config',
	currentConfigPage: 'options',
	settingsLoading: false,
};

const settingsState = {
	webTitle: 'IpacPanel',
	listen: '127.0.0.1:25555',
	webEnableHttps: false,
	webForceHttps: false,
	webPrivateKeyPath: './data/cert/key',
	webPublicKeyPath: './data/cert/pem',
	historySize: 27,
	autoStartInterval: 200,
	autoRestartInterval: 1000,
	instanceUpdateStagingDir: './!InstanceUpdate/',
	trustedProxyIps: ['127.0.0.1'],
	metricsEnabled: true,
	metricsPublicDashboard: false,
	metricsMemoryMaxMin: 30,
	metricsStorageMode: 'memory',
	metricsSqliteMaxDay: 7,
	metricsSqliteCompactAfterDay: 2,
	powEnabled: true,
	powTaskCount: 24,
	powDifficulty: 3,
	powTimestampMaxSkew: 90,
	debug: false,
};

const DEFAULT_WEB_TITLE = 'IpacPanel';
const DEFAULT_LISTEN = '127.0.0.1:25555';
const DEFAULT_WEB_PRIVATE_KEY_PATH = './data/cert/key';
const DEFAULT_WEB_PUBLIC_KEY_PATH = './data/cert/pem';
const DEFAULT_INSTANCE_UPDATE_STAGING_DIR = './!InstanceUpdate/';
const MIN_HISTORY_SIZE = 2;
const MAX_HISTORY_SIZE = 65536;
const MAX_INTERVAL_MS = 86400000;
const MAX_METRICS_MEMORY_MIN = 10080;
const MAX_METRICS_SQLITE_DAY = 36500;
const MAX_POW_TASK_COUNT = 128;
const MAX_POW_DIFFICULTY = 10;
const MAX_POW_TIMESTAMP_SKEW = 3600;

const normalizeWebTitle = (value) => {
	const title = String(value || '').trim();
	return title || DEFAULT_WEB_TITLE;
};

const normalizeListen = (value) => {
	const listen = String(value || '').trim();
	return listen || DEFAULT_LISTEN;
};

const clampInteger = (value, fallback, minValue, maxValue) => {
	const number = Number(value);
	if (!Number.isFinite(number)) return fallback;
	return Math.min(maxValue, Math.max(minValue, Math.trunc(number)));
};

const normalizeStringList = (value) => InputValidation.normalizeTrustedProxyIps(value);

const normalizeTrustedProxyIpsInput = () => {
	const value = normalizeStringList(dom.trustedProxyIps?.value || '').join('\n');
	if (dom.trustedProxyIps) dom.trustedProxyIps.value = value;
	return value;
};

const truncateInputValue = (input, maxLength) => {
	const value = InputValidation.truncateText(input?.value || '', maxLength).trim();
	if (input) input.value = value;
	return value;
};

const normalizeMetricsStorageMode = (value) => {
	const mode = String(value || '').trim().toLowerCase();
	return mode === 'sqlite' ? 'sqlite' : 'memory';
};

const setDocumentTitle = (title) => {
	return applyWebTitle(normalizeWebTitle(title));
};

const setSettingsStatus = (text, error = false) => {
	if (!dom.settingsStatus) return;
	dom.settingsStatus.textContent = String(text || '');
	dom.settingsStatus.classList.toggle('error', !!error);
};

const rejectSettingsField = (message, input) => {
	setSettingsStatus(message, true);
	input?.focus?.();
	return false;
};

const settingsFieldInputs = {
	webTitle: () => dom.webTitle,
	listen: () => dom.listen,
	webPrivateKeyPath: () => dom.webPrivateKeyPath,
	webPublicKeyPath: () => dom.webPublicKeyPath,
	instanceUpdateStagingDir: () => dom.instanceUpdateStagingDir,
	trustedProxyIps: () => dom.trustedProxyIps,
};

const settingsFieldConfigPages = {
	webTitle: 'options',
	listen: 'options',
	instanceUpdateStagingDir: 'options',
	trustedProxyIps: 'options',
	webPrivateKeyPath: 'web',
	webPublicKeyPath: 'web',
};

const rejectSettingsValidation = (result) => {
	if (result?.ok !== false) {
		return false;
	}
	const page = settingsFieldConfigPages[result.field];
	if (page) applyConfigPage(page);
	const input = settingsFieldInputs[result.field]?.();
	if (page === 'web') {
		setWebStatus(result.message || 'WEB SETTINGS INVALID', true);
		input?.focus?.();
		return false;
	}
	return rejectSettingsField(result.message || 'SETTINGS INVALID', input);
};

const readClampedInteger = (input, fallback, minValue, maxValue) => {
	const raw = String(input?.value ?? '').trim();
	const value = Number.parseInt(raw, 10);
	if (raw === '' || !Number.isInteger(value)) {
		return fallback;
	}
	return Math.min(maxValue, Math.max(minValue, value));
};

const showInputLimitAlert = async (message, input, setStatus) => {
	setStatus(message, true);
	input?.focus?.();
	await showAlert(message, { title: 'INVALID', okText: 'OK' });
	return false;
};

const syncMetricsStorageModeFields = () => {
	const storageMode = normalizeMetricsStorageMode(dom.metricsStorageMode?.value);
	dom.metricsMemoryGroup?.classList.toggle('hidden', storageMode !== 'memory');
	dom.metricsSqliteGroup?.classList.toggle('hidden', storageMode !== 'sqlite');
};

const setPowStatus = (text, error = false) => {
	if (!dom.powStatus) return;
	dom.powStatus.textContent = String(text || '');
	dom.powStatus.classList.toggle('error', !!error);
};

const setMetricsStatus = (text, error = false) => {
	if (!dom.metricsStatus) return;
	dom.metricsStatus.textContent = String(text || '');
	dom.metricsStatus.classList.toggle('error', !!error);
};

const setWebStatus = (text, error = false) => {
	if (!dom.webStatus) return;
	dom.webStatus.textContent = String(text || '');
	dom.webStatus.classList.toggle('error', !!error);
};

const setDebugStatus = (text, error = false) => {
	if (!dom.debugStatus) return;
	dom.debugStatus.textContent = String(text || '');
	dom.debugStatus.classList.toggle('error', !!error);
};

const setSettingsSaveDisabled = (disabled) => {
	[dom.settingsSave, dom.metricsSave, dom.webSave, dom.powSave, dom.debugSave].forEach((button) => {
		if (button) button.disabled = !!disabled;
	});
};

const clearSettingsFormForLoad = () => {
	[
		dom.webTitle,
		dom.listen,
		dom.webPrivateKeyPath,
		dom.webPublicKeyPath,
		dom.historySize,
		dom.autoStartInterval,
		dom.autoRestartInterval,
		dom.instanceUpdateStagingDir,
		dom.trustedProxyIps,
		dom.metricsMemoryMaxMin,
		dom.metricsSqliteMaxDay,
		dom.metricsSqliteCompactAfterDay,
		dom.powTaskCount,
		dom.powDifficulty,
		dom.powTimestampMaxSkew,
	].forEach((input) => {
		if (input) input.value = '';
	});
	if (dom.powEnabled) dom.powEnabled.checked = false;
	if (dom.webEnableHttps) dom.webEnableHttps.checked = false;
	if (dom.webForceHttps) dom.webForceHttps.checked = false;
	if (dom.metricsEnabled) dom.metricsEnabled.checked = false;
	if (dom.metricsPublicDashboard) dom.metricsPublicDashboard.checked = false;
	if (dom.metricsStorageMode) dom.metricsStorageMode.value = 'memory';
	if (dom.debugEnabled) dom.debugEnabled.checked = false;
	setSettingsStatus('LOADING...');
	setMetricsStatus('LOADING...');
	setWebStatus('LOADING...');
	setPowStatus('LOADING...');
	setDebugStatus('LOADING...');
};

const beginSettingsLoad = () => {
	panelSettingsState.settingsLoading = true;
	clearSettingsFormForLoad();
	setSettingsSaveDisabled(true);
};

const finishSettingsLoad = (loaded) => {
	panelSettingsState.settingsLoading = false;
	setSettingsSaveDisabled(!loaded);
};

const applyConfigPage = (page) => {
	const targetPage = page === 'metrics' || page === 'web' || page === 'pow' ? page : 'options';
	panelSettingsState.currentConfigPage = targetPage;
	dom.optionsConfigTab?.classList.toggle('active', targetPage === 'options');
	dom.metricsConfigTab?.classList.toggle('active', targetPage === 'metrics');
	dom.webConfigTab?.classList.toggle('active', targetPage === 'web');
	dom.powConfigTab?.classList.toggle('active', targetPage === 'pow');
	dom.optionsPage?.classList.toggle('active', targetPage === 'options');
	dom.metricsPage?.classList.toggle('active', targetPage === 'metrics');
	dom.webPage?.classList.toggle('active', targetPage === 'web');
	dom.powPage?.classList.toggle('active', targetPage === 'pow');
};

const applyMainPage = (page, configPage = panelSettingsState.currentConfigPage) => {
	const targetPage = page === 'upload' || page === 'debug' ? page : 'config';
	panelSettingsState.currentMainPage = targetPage;
	dom.configMainTab?.classList.toggle('active', targetPage === 'config');
	dom.uploadMainTab?.classList.toggle('active', targetPage === 'upload');
	dom.debugMainTab?.classList.toggle('active', targetPage === 'debug');
	dom.configTabs?.classList.toggle('hidden', targetPage !== 'config');
	if (targetPage === 'config') {
		applyConfigPage(configPage);
	} else {
		dom.optionsPage?.classList.remove('active');
		dom.metricsPage?.classList.remove('active');
		dom.webPage?.classList.remove('active');
		dom.powPage?.classList.remove('active');
	}
	dom.uploadPage?.classList.toggle('active', targetPage === 'upload');
	dom.debugPage?.classList.toggle('active', targetPage === 'debug');
};

const buildRuntimeSettingsSnapshot = () => ({
	webTitle: settingsState.webTitle,
	listen: settingsState.listen,
	web: {
		enableHttps: settingsState.webEnableHttps,
		forceHttps: settingsState.webForceHttps,
		privateKeyPath: settingsState.webPrivateKeyPath,
		publicKeyPath: settingsState.webPublicKeyPath,
	},
	historySize: settingsState.historySize,
	autoStartInterval: settingsState.autoStartInterval,
	autoRestartInterval: settingsState.autoRestartInterval,
	instanceUpdateStagingDir: settingsState.instanceUpdateStagingDir,
	trustedProxyIps: [...settingsState.trustedProxyIps],
	metrics: {
		enabled: settingsState.metricsEnabled,
		publicDashboard: settingsState.metricsPublicDashboard,
		memoryMaxMin: settingsState.metricsMemoryMaxMin,
		storageMode: settingsState.metricsStorageMode,
		sqliteMaxDay: settingsState.metricsSqliteMaxDay,
		sqliteCompactAfterDay: settingsState.metricsSqliteCompactAfterDay,
	},
	pow: {
		enabled: settingsState.powEnabled,
		taskCount: settingsState.powTaskCount,
		difficulty: settingsState.powDifficulty,
		timestampMaxSkew: settingsState.powTimestampMaxSkew,
	},
	debug: settingsState.debug,
});

const dispatchRuntimeSettingsApplied = (previousSettings) => {
	window.dispatchEvent(new CustomEvent('ipacpanel:settings-applied', {
		detail: {
			previous: previousSettings,
			current: buildRuntimeSettingsSnapshot(),
		},
	}));
};

const renderSettings = (data = {}, options = {}) => {
	const applyRuntime = options.applyRuntime !== false;
	const previousSettings = buildRuntimeSettingsSnapshot();
	settingsState.webTitle = normalizeWebTitle(data.web_title);
	settingsState.listen = normalizeListen(data.listen);
	settingsState.webEnableHttps = !!data.web?.enable_https;
	settingsState.webForceHttps = !!data.web?.force_https;
	settingsState.webPrivateKeyPath = String(data.web?.private_key_path || DEFAULT_WEB_PRIVATE_KEY_PATH).trim();
	settingsState.webPublicKeyPath = String(data.web?.public_key_path || DEFAULT_WEB_PUBLIC_KEY_PATH).trim();
	settingsState.historySize = clampInteger(data.history_size, 27, MIN_HISTORY_SIZE, MAX_HISTORY_SIZE);
	settingsState.autoStartInterval = clampInteger(data.auto_start_interval, 200, 0, MAX_INTERVAL_MS);
	settingsState.autoRestartInterval = clampInteger(data.auto_restart_interval, 1000, 0, MAX_INTERVAL_MS);
	settingsState.instanceUpdateStagingDir = String(data.instance_update_staging_dir || DEFAULT_INSTANCE_UPDATE_STAGING_DIR).trim();
	settingsState.trustedProxyIps = normalizeStringList(data.trusted_proxy_ips);
	const metrics = data.metrics || {};
	settingsState.metricsEnabled = !!metrics.enabled;
	settingsState.metricsPublicDashboard = !!metrics.public_dashboard;
	settingsState.metricsMemoryMaxMin = clampInteger(metrics.memory_max_min, 30, 1, MAX_METRICS_MEMORY_MIN);
	settingsState.metricsStorageMode = normalizeMetricsStorageMode(metrics.storage_mode);
	settingsState.metricsSqliteMaxDay = clampInteger(metrics.sqlite_max_day, 7, 0, MAX_METRICS_SQLITE_DAY);
	settingsState.metricsSqliteCompactAfterDay = clampInteger(metrics.sqlite_compact_after_day, 2, 0, MAX_METRICS_SQLITE_DAY);
	const pow = data.pow || {};
	settingsState.powEnabled = !!pow.enabled;
	settingsState.powTaskCount = clampInteger(pow.task_count, 24, 1, MAX_POW_TASK_COUNT);
	settingsState.powDifficulty = clampInteger(pow.difficulty, 3, 1, MAX_POW_DIFFICULTY);
	settingsState.powTimestampMaxSkew = clampInteger(pow.timestamp_max_skew, 90, 1, MAX_POW_TIMESTAMP_SKEW);
	settingsState.debug = !!data.debug;
	if (dom.webTitle) {
		dom.webTitle.value = settingsState.webTitle;
	}
	if (dom.listen) dom.listen.value = settingsState.listen;
	if (dom.webEnableHttps) dom.webEnableHttps.checked = settingsState.webEnableHttps;
	if (dom.webForceHttps) dom.webForceHttps.checked = settingsState.webForceHttps;
	if (dom.webPrivateKeyPath) dom.webPrivateKeyPath.value = settingsState.webPrivateKeyPath;
	if (dom.webPublicKeyPath) dom.webPublicKeyPath.value = settingsState.webPublicKeyPath;
	if (dom.historySize) dom.historySize.value = String(settingsState.historySize);
	if (dom.autoStartInterval) dom.autoStartInterval.value = String(settingsState.autoStartInterval);
	if (dom.autoRestartInterval) dom.autoRestartInterval.value = String(settingsState.autoRestartInterval);
	if (dom.instanceUpdateStagingDir) dom.instanceUpdateStagingDir.value = settingsState.instanceUpdateStagingDir;
	if (dom.trustedProxyIps) dom.trustedProxyIps.value = settingsState.trustedProxyIps.join('\n');
	if (dom.metricsEnabled) dom.metricsEnabled.checked = settingsState.metricsEnabled;
	if (dom.metricsPublicDashboard) dom.metricsPublicDashboard.checked = settingsState.metricsPublicDashboard;
	if (dom.metricsMemoryMaxMin) dom.metricsMemoryMaxMin.value = String(settingsState.metricsMemoryMaxMin);
	if (dom.metricsStorageMode) dom.metricsStorageMode.value = settingsState.metricsStorageMode;
	if (dom.metricsSqliteMaxDay) dom.metricsSqliteMaxDay.value = String(settingsState.metricsSqliteMaxDay);
	if (dom.metricsSqliteCompactAfterDay) dom.metricsSqliteCompactAfterDay.value = String(settingsState.metricsSqliteCompactAfterDay);
	syncMetricsStorageModeFields();
	if (dom.powEnabled) dom.powEnabled.checked = settingsState.powEnabled;
	if (dom.powTaskCount) dom.powTaskCount.value = String(settingsState.powTaskCount);
	if (dom.powDifficulty) dom.powDifficulty.value = String(settingsState.powDifficulty);
	if (dom.powTimestampMaxSkew) dom.powTimestampMaxSkew.value = String(settingsState.powTimestampMaxSkew);
	if (dom.debugEnabled) {
		dom.debugEnabled.checked = settingsState.debug;
	}
	setDocumentTitle(settingsState.webTitle);
	setSettingsStatus('');
	setMetricsStatus('');
	setWebStatus('');
	setPowStatus('');
	setDebugStatus('');
	if (applyRuntime) {
		dispatchRuntimeSettingsApplied(previousSettings);
	}
};

const refreshSettings = async () => {
	const result = await fetchSettings();
	if (!result.ok) {
		setSettingsStatus(result.error || 'LOAD SETTINGS FAILED', true);
		setMetricsStatus('LOAD SETTINGS FAILED', true);
		setWebStatus('LOAD SETTINGS FAILED', true);
		setPowStatus('LOAD SETTINGS FAILED', true);
		setDebugStatus('LOAD SETTINGS FAILED', true);
		return false;
	}
	renderSettings(result.data || {}, { applyRuntime: false });
	return true;
};

const buildConfigSettingsPayload = () => {
	const webTitle = normalizeWebTitle(truncateInputValue(dom.webTitle, InputValidation.settings.limits.webTitle));
	const listen = normalizeListen(truncateInputValue(dom.listen, InputValidation.settings.limits.listen));
	const instanceUpdateStagingDir = truncateInputValue(dom.instanceUpdateStagingDir, InputValidation.settings.limits.instanceUpdateStagingDir);
	const trustedProxyIpsText = normalizeTrustedProxyIpsInput();
	const validationResult = InputValidation.settings.validateGeneralTextFields({ webTitle, listen, instanceUpdateStagingDir, trustedProxyIpsText });
	if (!validationResult.ok) {
		return { ok: false, validationResult };
	}
	const webPrivateKeyPath = truncateInputValue(dom.webPrivateKeyPath, InputValidation.settings.limits.webPrivateKeyPath);
	const webPublicKeyPath = truncateInputValue(dom.webPublicKeyPath, InputValidation.settings.limits.webPublicKeyPath);
	const webValidationResult = InputValidation.settings.validateWebTextFields({ webPrivateKeyPath, webPublicKeyPath });
	if (!webValidationResult.ok) {
		return { ok: false, validationResult: webValidationResult };
	}
	const historySize = readClampedInteger(dom.historySize, 27, MIN_HISTORY_SIZE, MAX_HISTORY_SIZE);
	const autoStartInterval = readClampedInteger(dom.autoStartInterval, 200, 0, MAX_INTERVAL_MS);
	const autoRestartInterval = readClampedInteger(dom.autoRestartInterval, 1000, 0, MAX_INTERVAL_MS);
	const trustedProxyIps = normalizeStringList(trustedProxyIpsText);
	const storageMode = normalizeMetricsStorageMode(dom.metricsStorageMode.value);
	const memoryMaxMin = readClampedInteger(dom.metricsMemoryMaxMin, 30, 1, MAX_METRICS_MEMORY_MIN);
	const sqliteMaxDay = readClampedInteger(dom.metricsSqliteMaxDay, 7, 0, MAX_METRICS_SQLITE_DAY);
	const sqliteCompactAfterDay = readClampedInteger(dom.metricsSqliteCompactAfterDay, 2, 0, MAX_METRICS_SQLITE_DAY);
	const taskCount = readClampedInteger(dom.powTaskCount, 24, 1, MAX_POW_TASK_COUNT);
	const difficulty = readClampedInteger(dom.powDifficulty, 3, 1, MAX_POW_DIFFICULTY);
	const timestampMaxSkew = readClampedInteger(dom.powTimestampMaxSkew, 90, 1, MAX_POW_TIMESTAMP_SKEW);
	return {
		ok: true,
		payload: {
			web_title: webTitle,
			listen,
			history_size: historySize,
			auto_start_interval: autoStartInterval,
			auto_restart_interval: autoRestartInterval,
			instance_update_staging_dir: instanceUpdateStagingDir,
			trusted_proxy_ips: trustedProxyIps,
			web: {
				enable_https: dom.webEnableHttps.checked,
				force_https: dom.webForceHttps.checked,
				private_key_path: webPrivateKeyPath,
				public_key_path: webPublicKeyPath,
			},
			metrics: {
				enabled: dom.metricsEnabled.checked,
				public_dashboard: !!dom.metricsPublicDashboard.checked,
				storage_mode: storageMode,
				memory_max_min: memoryMaxMin,
				sqlite_max_day: sqliteMaxDay,
				sqlite_compact_after_day: sqliteCompactAfterDay,
			},
			pow: {
				enabled: dom.powEnabled.checked,
				task_count: taskCount,
				difficulty,
				timestamp_max_skew: timestampMaxSkew,
			},
		},
	};
};

const saveConfigSettings = async (setStatus, fallbackError) => {
	if (panelSettingsState.settingsLoading) return false;
	const payloadResult = buildConfigSettingsPayload();
	if (!payloadResult.ok) {
		rejectSettingsValidation(payloadResult.validationResult);
		return false;
	}
	const result = await updateSettings(payloadResult.payload);
	if (!result.ok) {
		setStatus(result.error || fallbackError, true);
		return false;
	}
	renderSettings(result.data || payloadResult.payload);
	setStatus('SAVED');
	closeModal();
	return true;
};

const saveSettings = async () => {
	return await saveConfigSettings(setSettingsStatus, 'SAVE SETTINGS FAILED');
};

const saveWebSettings = async () => {
	return await saveConfigSettings(setWebStatus, 'SAVE WEB FAILED');
};

const saveMetricsSettings = async () => {
	return await saveConfigSettings(setMetricsStatus, 'SAVE METRICS FAILED');
};

const savePowSettings = async () => {
	return await saveConfigSettings(setPowStatus, 'SAVE POW FAILED');
};

const saveDebugSettings = async () => {
	if (panelSettingsState.settingsLoading) return false;
	const debug = dom.debugEnabled.checked;
	const result = await updateSettings({ debug });
	if (!result.ok) {
		setDebugStatus(result.error || 'SAVE DEBUG FAILED', true);
		return false;
	}
	renderSettings(result.data || { web_title: settingsState.webTitle, debug });
	setDebugStatus('SAVED');
	closeModal();
	return true;
};

const restartControllerFromSettings = async () => {
	const ok = await showConfirm('确认后管理进程会重启. 实例进程会继续运行, 页面可能短暂断开.', { title: 'RESTART CONTROLLER', okText: 'RESTART' });
	if (!ok) return false;
	setDebugStatus('RESTARTING');
	const result = await restartController();
	if (!result.ok) {
		setDebugStatus(result.error || 'RESTART FAILED', true);
		return false;
	}
	void showAlert('管理进程正在重启, 请稍候后刷新页面或等待页面自动恢复.', { title: 'RESTARTING', okText: 'OK' });
	return true;
};

const setActionStatus = (text, error = false) => {
	if (!dom.actionStatus) return;
	dom.actionStatus.textContent = String(text || '');
	dom.actionStatus.classList.toggle('error', !!error);
};

const getErrorMessage = (error, fallback) => {
	if (error instanceof Error && error.message) return error.message;
	const message = String(error || '').trim();
	return message || fallback;
};

const renderControllerUpdateActions = () => {
	if (dom.apply) {
		dom.apply.disabled = panelSettingsState.locked || (!panelSettingsState.pending && !panelSettingsState.selectedFile);
		dom.apply.textContent = panelSettingsState.selectedFile ? 'UPLOAD' : 'UPDATE';
	}
	if (dom.cancel) dom.cancel.disabled = panelSettingsState.locked;
};

const setLocked = (locked) => {
	panelSettingsState.locked = !!locked;
	dom.dropzone?.classList.toggle('locked', panelSettingsState.locked);
	renderControllerUpdateActions();
};

const setUploadError = (text = '') => {
	if (!dom.error) return;
	const message = String(text || '').trim();
	dom.error.textContent = message;
	dom.error.classList.toggle('hidden', !message);
};

const validateControllerUpdateFile = (file) => {
	if (!file) {
		return '请选择更新压缩包.';
	}
	const name = String(file.name || '').trim();
	if (!name.toLowerCase().endsWith('.zip')) {
		return '管理进程更新只支持 .zip 压缩包.';
	}
	return '';
};

const renderEmptyControllerUpdate = () => {
	dom.progressWrap?.classList.add('hidden');
	if (dom.fileName) dom.fileName.textContent = '';
	if (dom.loaded) dom.loaded.textContent = '0 B / 0 B';
	if (dom.percent) dom.percent.textContent = '0%';
	if (dom.status) dom.status.textContent = 'WAITING';
	if (dom.progress) dom.progress.style.width = '0%';
	setUploadError('');
};

const renderPendingControllerUpdate = () => {
	dom.progressWrap?.classList.remove('hidden');
	if (dom.fileName) dom.fileName.textContent = panelSettingsState.updateFileName;
	if (dom.loaded) dom.loaded.textContent = `${formatFileSize(panelSettingsState.updateFileSize)} / ${formatFileSize(panelSettingsState.updateFileSize)}`;
	if (dom.percent) dom.percent.textContent = '100%';
	if (dom.status) dom.status.textContent = 'READY';
	if (dom.progress) dom.progress.style.width = '100%';
	setUploadError('');
};

const renderStatus = (data = {}, options = {}) => {
	panelSettingsState.pending = !!data.pending;
	if (panelSettingsState.pending) {
		panelSettingsState.updateFileName = String(data.name || panelSettingsState.updateFileName).trim();
		panelSettingsState.updateFileSize = Math.max(0, Number(data.size || panelSettingsState.updateFileSize || 0));
	} else {
		panelSettingsState.updateFileName = '';
		panelSettingsState.updateFileSize = 0;
	}
	if (options.clearSelectedFile === true) {
		panelSettingsState.selectedFile = null;
	}
	if (!panelSettingsState.selectedFile) {
		if (panelSettingsState.pending) {
			renderPendingControllerUpdate();
		} else {
			renderEmptyControllerUpdate();
		}
	}
	renderControllerUpdateActions();
};

const stageControllerUpdateFile = (file) => {
	const validationError = validateControllerUpdateFile(file);
	if (validationError) {
		panelSettingsState.selectedFile = null;
		if (panelSettingsState.pending) {
			renderPendingControllerUpdate();
		} else {
			renderEmptyControllerUpdate();
		}
		setUploadError(validationError);
		setActionStatus('SELECT FAILED', true);
		renderControllerUpdateActions();
		return false;
	}
	panelSettingsState.selectedFile = file;
	updateProgress(file, 0, 'WAITING');
	setActionStatus('READY TO UPLOAD');
	renderControllerUpdateActions();
	return true;
};

const refreshStatus = async () => {
	const result = await fetchControllerUpdateStatus();
	if (!result.ok) {
		setActionStatus(result.error || 'LOAD STATUS FAILED', true);
		return;
	}
	renderStatus(result.data || {});
};

const closeModal = () => {
	if (panelSettingsState.locked) return;
	dom.modal?.classList.add('closing');
	panelSettingsState.closeTimer = setTimeout(() => {
		dom.modal?.classList.remove('visible', 'closing');
		dom.modal && (dom.modal.style.display = 'none');
	}, 240);
};

const updateProgress = (file, loaded, status = 'UPLOADING') => {
	const total = Math.max(0, file.size || 0);
	const safeLoaded = Math.max(0, Math.min(total, loaded || 0));
	const percent = total > 0 ? Math.min(100, Math.round((safeLoaded / total) * 100)) : 100;
	dom.progressWrap?.classList.remove('hidden');
	if (dom.fileName) dom.fileName.textContent = file.name || '';
	if (dom.loaded) dom.loaded.textContent = `${formatFileSize(safeLoaded)} / ${formatFileSize(total)}`;
	if (dom.percent) dom.percent.textContent = `${percent}%`;
	if (dom.status) dom.status.textContent = status;
	if (dom.progress) dom.progress.style.width = `${percent}%`;
	setUploadError('');
};

const uploadFile = async (file) => {
	const validationError = validateControllerUpdateFile(file);
	if (validationError) {
		setUploadError(validationError);
		setActionStatus('UPLOAD FAILED', true);
		return false;
	}
	panelSettingsState.updateFileName = String(file.name || '').trim();
	const chunkCount = Math.max(1, Math.ceil(file.size / CHUNK_SIZE));
	const chunkSize = Math.min(Math.max(file.size, 1), CHUNK_SIZE);
	setLocked(true);
	try {
		const init = await initControllerUpdateUpload({
			name: panelSettingsState.updateFileName,
			size: file.size,
			chunk_size: chunkSize,
			chunk_count: chunkCount,
		});
		const uploadId = init ? init.upload_id : '';
		if (!uploadId) throw new Error('UPLOAD INIT FAILED');
		let uploaded = 0;
		for (let index = 0; index < chunkCount; index += 1) {
			const start = index * chunkSize;
			const end = Math.min(file.size, start + chunkSize);
			const chunk = file.slice(start, end);
			let chunkLoaded = 0;
			await uploadControllerUpdateChunk(uploadId, index, chunk, (loaded) => {
				chunkLoaded = Math.max(0, Math.min(chunk.size, loaded || 0));
				updateProgress(file, uploaded + chunkLoaded);
			});
			uploaded += chunk.size;
			updateProgress(file, uploaded);
		}
		updateProgress(file, file.size, 'VERIFYING');
		const completed = await completeControllerUpdateUpload(uploadId);
		renderStatus({ ...completed, pending: true, name: panelSettingsState.updateFileName, size: file.size }, { clearSelectedFile: true });
		setActionStatus('VERIFY PASSED');
		return true;
	} catch (error) {
		console.error('[面板更新] 上传失败:', error);
		setUploadError(getUploadErrorText(error));
		setActionStatus('UPLOAD FAILED', true);
		return false;
	} finally {
		setLocked(false);
	}
};

const applyControllerUpdateFromSettings = async () => {
	setLocked(true);
	try {
		await applyControllerUpdate();
		setActionStatus('RESTARTING');
		void showAlert('管理进程正在重启, 请稍候后刷新页面或等待页面自动恢复.', { title: 'RESTARTING', okText: 'OK' });
		return true;
	} catch (error) {
		setActionStatus(getErrorMessage(error, 'UPDATE FAILED'), true);
		setLocked(false);
		return false;
	}
};

const uploadSelectedFileAndApply = async () => {
	const file = panelSettingsState.selectedFile;
	if (!file) return false;
	const ok = await showConfirm('确认后将上传更新, 管理进程将会重启, 实例进程保持运行.', { title: 'UPDATE', okText: 'UPDATE' });
	if (!ok) return false;
	const uploaded = await uploadFile(file);
	if (!uploaded) return false;
	panelSettingsState.selectedFile = null;
	setActionStatus('UPDATING');
	return await applyControllerUpdateFromSettings();
};

const bindEvents = () => {
	if (panelSettingsState.isBound) return;
	panelSettingsState.isBound = true;
	dom.close?.addEventListener('click', closeModal);
	dom.configMainTab?.addEventListener('click', () => applyMainPage('config'));
	dom.uploadMainTab?.addEventListener('click', () => applyMainPage('upload'));
	dom.debugMainTab?.addEventListener('click', () => applyMainPage('debug'));
	dom.optionsConfigTab?.addEventListener('click', () => applyConfigPage('options'));
	dom.metricsConfigTab?.addEventListener('click', () => applyConfigPage('metrics'));
	dom.webConfigTab?.addEventListener('click', () => applyConfigPage('web'));
	dom.powConfigTab?.addEventListener('click', () => applyConfigPage('pow'));
	dom.settingsCancel?.addEventListener('click', closeModal);
	dom.metricsCancel?.addEventListener('click', closeModal);
	dom.webCancel?.addEventListener('click', closeModal);
	dom.powCancel?.addEventListener('click', closeModal);
	dom.debugCancel?.addEventListener('click', closeModal);
	dom.optionsPage?.addEventListener('submit', (event) => {
		event.preventDefault();
		void withActionsDisabled(dom.optionsPage.querySelector('.modal-actions'), saveSettings);
	});
	dom.powPage?.addEventListener('submit', (event) => {
		event.preventDefault();
		void withActionsDisabled(dom.powPage.querySelector('.modal-actions'), savePowSettings);
	});
	dom.metricsPage?.addEventListener('submit', (event) => {
		event.preventDefault();
		void withActionsDisabled(dom.metricsPage.querySelector('.modal-actions'), saveMetricsSettings);
	});
	dom.webPage?.addEventListener('submit', (event) => {
		event.preventDefault();
		void withActionsDisabled(dom.webPage.querySelector('.modal-actions'), saveWebSettings);
	});
	dom.metricsStorageMode?.addEventListener('change', syncMetricsStorageModeFields);
	dom.trustedProxyIps?.addEventListener('blur', normalizeTrustedProxyIpsInput);
	dom.debugSave?.addEventListener('click', () => {
		void withActionsDisabled(dom.debugPage?.querySelector('.modal-actions'), saveDebugSettings);
	});
	dom.restartController?.addEventListener('click', () => {
		void withActionsDisabled(dom.restartController?.parentElement, restartControllerFromSettings);
	});
	dom.dropzone?.addEventListener('click', () => {
		if (!panelSettingsState.locked) dom.fileInput?.click();
	});
	dom.fileInput?.addEventListener('change', () => {
		const file = dom.fileInput?.files?.[0] || null;
		dom.fileInput.value = '';
		stageControllerUpdateFile(file);
	});
	dom.dropzone?.addEventListener('dragover', (event) => {
		event.preventDefault();
		if (panelSettingsState.locked) return;
		dom.dropzone?.classList.add('dragover');
	});
	dom.dropzone?.addEventListener('dragleave', () => dom.dropzone?.classList.remove('dragover'));
	dom.dropzone?.addEventListener('drop', (event) => {
		event.preventDefault();
		dom.dropzone?.classList.remove('dragover');
		if (panelSettingsState.locked) return;
		const dataTransfer = event.dataTransfer;
		stageControllerUpdateFile(dataTransfer && dataTransfer.files ? dataTransfer.files[0] || null : null);
	});
	dom.cancel?.addEventListener('click', closeModal);
	dom.apply?.addEventListener('click', async () => {
		if (panelSettingsState.selectedFile) {
			void uploadSelectedFileAndApply();
			return;
		}
		if (!panelSettingsState.pending) return;
		const ok = await showConfirm('确认后将上传更新, 管理进程将会重启, 实例进程保持运行.', { title: 'UPDATE', okText: 'UPDATE' });
		if (!ok) return;
		void applyControllerUpdateFromSettings();
	});
};

export const bootPanelSettingsModal = () => {
	bindEvents();
	return {
		open: async () => {
			panelSettingsState.closeTimer = clearTimer(panelSettingsState.closeTimer);
			applyMainPage('config', 'options');
			beginSettingsLoad();
			dom.modal.style.display = 'flex';
			dom.modal.classList.remove('closing');
			requestAnimationFrame(() => dom.modal?.classList.add('visible'));
			const loaded = await refreshSettings();
			finishSettingsLoad(loaded);
			setActionStatus('');
			void refreshStatus();
			if (loaded) dom.webTitle?.focus();
		},
	};
};
