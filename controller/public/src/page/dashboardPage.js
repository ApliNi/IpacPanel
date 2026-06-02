import { openDashboardEventStream } from '../api/dashboard.js';
import { dispatchUnauthorized, parseSSEJsonData, readSSEStream } from '../api/core.js';
import { mainContainer, state } from '../ui.js';

console.log('[页面] 仪表板页加载中...');

const STORAGE_MINUTES_KEY = 'IpacPanel.dashboard.minutes';
const STORAGE_INTERFACE_KEY = 'IpacPanel.dashboard.interface';
const STORAGE_DISK_KEY = 'IpacPanel.dashboard.disk';
const STORAGE_DETAIL_MODE_KEY = 'IpacPanel.dashboard.detailMode';
const DASHBOARD_PAGE_SYSTEM = 'system';
const DASHBOARD_PAGE_IPAC_PANEL = 'ipacPanel';
const DEFAULT_MINUTES = 30;
const X_AXIS_TARGET_GRID_SPACE = 92;
const CHART_LINE_WIDTH = 1;
const DETAIL_SCALE_PADDING_RATIO = 0.08;
const Y_AXIS_FIXED_SIZE = (4 + 1 + 2) * 7 + 18;
const CHART_BLUE = '#17d9ff';
const CHART_BLUE_FILL = 'rgba(23,217,255,0.12)';
const CHART_YELLOW = '#f4b740';
const HIDDEN_POINTS = { show: false };
const DATA_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB'];
const SELECT_SEPARATOR_VALUE = '__separator__';
const RECONNECT_BASE_DELAY_MS = 400;
const RECONNECT_MAX_DELAY_MS = 5000;
const RECONNECT_JITTER_RATIO = 0.2;
const DASHBOARD_SAMPLE_SCHEMA = ['dt', 'cpu10', 'mem_used', 'mem_total', 'swap_used', 'swap_total', 'net_rx_bps', 'net_tx_bps', 'disk_read_bps', 'disk_write_bps', 'tcp_conn', 'udp_conn'];

mainContainer.insertAdjacentHTML('beforeend', /*html*/`
	<section id="dashboardSection" class="section dashboard-section">
		<div class="section-header dashboard-section-header">
			<div class="filter-group dashboard-page-tabs">
				<button id="dashboardPageTabSystem" class="filter-btn active" type="button" data-page="${DASHBOARD_PAGE_SYSTEM}">SYSTEM</button>
				<button id="dashboardPageTabIpacPanel" class="filter-btn" type="button" data-page="${DASHBOARD_PAGE_IPAC_PANEL}">IpacPanel</button>
			</div>
			<div class="dashboard-controls">
				<div class="filter-group dashboard-filter-group">
					<label for="dashboardMinutesInput" class="dashboard-control-label">MIN</label>
					<input id="dashboardMinutesInput" class="dashboard-number-input" type="number" min="1" max="10080" step="1" autocomplete="off" inputmode="numeric">
				</div>
				<div id="dashboardDiskFilter" class="filter-group dashboard-filter-group">
					<label for="dashboardDiskSelect" class="dashboard-control-label">DISK</label>
					<div class="select-wrapper dashboard-select-wrapper">
						<select id="dashboardDiskSelect" class="dashboard-select"></select>
					</div>
				</div>
				<div id="dashboardInterfaceFilter" class="filter-group dashboard-filter-group">
					<label for="dashboardInterfaceSelect" class="dashboard-control-label">NIC</label>
					<div class="select-wrapper dashboard-select-wrapper">
						<select id="dashboardInterfaceSelect" class="dashboard-select"></select>
					</div>
				</div>
			</div>
		</div>
		<div id="dashboardSystemPage" class="dashboard-content dashboard-page active">
			<div id="dashboardError" class="file-action-static dashboard-error hidden"></div>
			<div class="dashboard-chart-grid">
				<article class="dashboard-chart-card">
					<header class="dashboard-chart-header">
						<div class="dashboard-chart-label">CPU</div>
					</header>
					<div id="dashboardCpuChart" class="dashboard-chart"></div>
				</article>
				<article class="dashboard-chart-card">
					<header class="dashboard-chart-header">
						<div class="dashboard-chart-label">RAM</div>
					</header>
					<div id="dashboardMemoryChart" class="dashboard-chart"></div>
				</article>
				<article class="dashboard-chart-card dashboard-chart-card-wide">
					<header class="dashboard-chart-header">
						<div class="dashboard-chart-label">DISK</div>
					</header>
					<div id="dashboardDiskChart" class="dashboard-chart"></div>
				</article>
				<article class="dashboard-chart-card dashboard-chart-card-wide">
					<header class="dashboard-chart-header">
						<div class="dashboard-chart-label">NET</div>
					</header>
					<div id="dashboardNetworkChart" class="dashboard-chart"></div>
				</article>
				<article class="dashboard-chart-card dashboard-chart-card-wide">
					<header class="dashboard-chart-header">
						<div class="dashboard-chart-label">CONN</div>
					</header>
					<div id="dashboardConnectionChart" class="dashboard-chart"></div>
				</article>
			</div>
		</div>
		<div id="dashboardIpacPanelPage" class="dashboard-content dashboard-page">
			<div class="dashboard-placeholder-card file-action-static">IpacPanel 指标暂未配置.</div>
		</div>
	</section>
`);

const dom = {
	section: document.getElementById('dashboardSection'),
	pageTabs: document.querySelectorAll('#dashboardSection .dashboard-page-tabs .filter-btn'),
	systemPage: document.getElementById('dashboardSystemPage'),
	ipacPanelPage: document.getElementById('dashboardIpacPanelPage'),
	controls: document.querySelector('#dashboardSection .dashboard-controls'),
	minutesInput: document.getElementById('dashboardMinutesInput'),
	interfaceFilter: document.getElementById('dashboardInterfaceFilter'),
	interfaceSelect: document.getElementById('dashboardInterfaceSelect'),
	diskFilter: document.getElementById('dashboardDiskFilter'),
	diskSelect: document.getElementById('dashboardDiskSelect'),
	error: document.getElementById('dashboardError'),
	cpuChart: document.getElementById('dashboardCpuChart'),
	memoryChart: document.getElementById('dashboardMemoryChart'),
	diskChart: document.getElementById('dashboardDiskChart'),
	networkChart: document.getElementById('dashboardNetworkChart'),
	connectionChart: document.getElementById('dashboardConnectionChart'),
};

const pageState = {
	active: false,
	activePage: DASHBOARD_PAGE_SYSTEM,
	stream: null,
	reconnectTimer: 0,
	reconnectAttempt: 0,
	publicUnauthorizedRetryUsed: false,
	lastSeq: 0,
	lastOptionsSeq: 0,
	baseTs: 0,
	fullLoading: false,
	fullScaleHints: null,
	preserveSamplesOnNextFull: false,
	pendingRender: false,
	pauseRefreshForCursor: false,
	legendEmptyHover: false,
	hoverTime: NaN,
	lastSamples: [],
	requestedMinutes: DEFAULT_MINUTES,
	displayMinutes: DEFAULT_MINUTES,
	selectedInterface: '',
	selectedDisk: '',
	detailMode: {
		cpu: false,
		memory: false,
		disk: false,
		network: false,
		connection: false,
	},
	charts: {
		cpu: null,
		memory: null,
		disk: null,
		network: null,
		connection: null,
	},
	crosshairs: new Map(),
	legendSyncFrame: 0,
	resizeObserver: null,
};

const getUPlotRuntime = () => {
	const UPlotCtor = window.uPlot;
	if (typeof UPlotCtor !== 'function') {
		throw new Error('uPlot 运行时不可用');
	}
	return UPlotCtor;
};

const clampMinutes = (value) => {
	const parsed = Number.parseInt(String(value), 10);
	if (!Number.isFinite(parsed) || parsed <= 0) {
		return DEFAULT_MINUTES;
	}
	return Math.min(parsed, 10080);
};

const parseSelectedMinutes = (value) => {
	const numeric = Number(value);
	if (!Number.isInteger(numeric) || numeric <= 0) {
		throw new Error('仪表板显示分钟数无效.');
	}
	return numeric;
};

const parseSelectedMinutesFromPayload = (payload) => {
	if (!payload || typeof payload !== 'object' || !Object.prototype.hasOwnProperty.call(payload, 'selected_minutes')) {
		throw new Error('仪表板显示分钟数缺失.');
	}
	return parseSelectedMinutes(payload.selected_minutes);
};

const readStoredMinutes = () => {
	try {
		return clampMinutes(localStorage.getItem(STORAGE_MINUTES_KEY) || DEFAULT_MINUTES);
	} catch (error) {
		console.error('[Dashboard] 读取分钟数配置失败:', error);
		return DEFAULT_MINUTES;
	}
};

const storeMinutes = (minutes) => {
	try {
		localStorage.setItem(STORAGE_MINUTES_KEY, String(clampMinutes(minutes)));
	} catch (error) {
		console.error('[Dashboard] 保存分钟数配置失败:', error);
	}
};

const readStoredText = (key) => {
	try {
		return String(localStorage.getItem(key) || '').trim();
	} catch (error) {
		console.error('[Dashboard] 读取本地配置失败:', error);
		return '';
	}
};

const storeText = (key, value) => {
	try {
		localStorage.setItem(key, String(value || '').trim());
	} catch (error) {
		console.error('[Dashboard] 保存本地配置失败:', error);
	}
};

const normalizeDashboardPage = (page) => {
	const normalized = String(page || '').trim();
	if (normalized === DASHBOARD_PAGE_IPAC_PANEL) {
		return DASHBOARD_PAGE_IPAC_PANEL;
	}
	return DASHBOARD_PAGE_SYSTEM;
};

const isSystemDashboardPageActive = () => pageState.activePage === DASHBOARD_PAGE_SYSTEM;

const isPublicDashboardMode = () => state.currentUser === null && state.publicDashboardEnabled === true;

const syncDashboardPage = () => {
	dom.pageTabs.forEach((tab) => {
		tab.classList.toggle('active', normalizeDashboardPage(tab.dataset.page) === pageState.activePage);
	});
	dom.systemPage.classList.toggle('active', isSystemDashboardPageActive());
	dom.ipacPanelPage.classList.toggle('active', pageState.activePage === DASHBOARD_PAGE_IPAC_PANEL);
	dom.controls.classList.toggle('hidden', !isSystemDashboardPageActive());
};

const setDashboardPage = (page) => {
	const nextPage = normalizeDashboardPage(page);
	if (pageState.activePage === nextPage) {
		syncDashboardPage();
		return;
	}
	pageState.activePage = nextPage;
	syncDashboardPage();
	if (!pageState.active) {
		return;
	}
	if (isSystemDashboardPageActive()) {
		restartDashboardStream();
		return;
	}
	stopDashboardStream();
	hideLinkedCrosshairs();
};

const canUseDashboardDeviceFilters = () => state.isAdmin === true;

const syncDashboardDeviceFilterVisibility = () => {
	const visible = canUseDashboardDeviceFilters();
	dom.diskFilter.classList.toggle('hidden', !visible);
	dom.interfaceFilter.classList.toggle('hidden', !visible);
	if (!visible) {
		pageState.selectedDisk = '';
		pageState.selectedInterface = '';
	}
};

const readStoredDetailMode = () => {
	try {
		return localStorage.getItem(STORAGE_DETAIL_MODE_KEY) === '1';
	} catch (error) {
		console.error('[Dashboard] 读取图表模式配置失败:', error);
		return false;
	}
};

const storeDetailMode = (enabled) => {
	try {
		localStorage.setItem(STORAGE_DETAIL_MODE_KEY, enabled ? '1' : '0');
	} catch (error) {
		console.error('[Dashboard] 保存图表模式配置失败:', error);
	}
};

const setAllDetailModes = (enabled) => {
	Object.keys(pageState.detailMode).forEach((modeKey) => {
		pageState.detailMode[modeKey] = enabled;
	});
};

const getReconnectDelay = (attempt) => {
	const safeAttempt = Math.max(0, Number(attempt) || 0);
	const rawDelay = Math.min(RECONNECT_MAX_DELAY_MS, RECONNECT_BASE_DELAY_MS * (2 ** Math.max(0, safeAttempt - 1)));
	const jitterFactor = 1 + ((Math.random() * 2 - 1) * RECONNECT_JITTER_RATIO);
	return Math.max(RECONNECT_BASE_DELAY_MS, Math.round(rawDelay * jitterFactor));
};

const formatBytes = (value) => formatScaled(value, DATA_UNITS, 1024, 1);
const formatBps = (value) => formatScaled(value, DATA_UNITS, 1024, 1);

const formatBytesCeil = (value) => {
	let numeric = Number(value);
	if (!Number.isFinite(numeric) || numeric < 0) {
		numeric = 0;
	}
	let unitIndex = 0;
	while (numeric >= 1024 && unitIndex < DATA_UNITS.length - 1) {
		numeric /= 1024;
		unitIndex += 1;
	}
	return `${Math.ceil(numeric)} ${DATA_UNITS[unitIndex]}`;
};

const ceilBytesToDisplayUnit = (value) => {
	let numeric = Number(value);
	if (!Number.isFinite(numeric) || numeric <= 0) {
		return 1;
	}
	let unitSize = 1;
	let unitIndex = 0;
	while (numeric >= 1024 && unitIndex < DATA_UNITS.length - 1) {
		numeric /= 1024;
		unitSize *= 1024;
		unitIndex += 1;
	}
	return Math.max(1, Math.ceil(numeric) * unitSize);
};

function formatScaled(value, units, base, precision) {
	let numeric = Number(value);
	if (!Number.isFinite(numeric) || numeric < 0) {
		numeric = 0;
	}
	let unitIndex = 0;
	while (numeric >= base && unitIndex < units.length - 1) {
		numeric /= base;
		unitIndex += 1;
	}
	const digits = numeric >= 100 || unitIndex === 0 ? 0 : precision;
	return `${numeric.toFixed(digits)} ${units[unitIndex]}`;
}

const formatPercent = (value) => {
	const numeric = Number(value);
	if (!Number.isFinite(numeric)) {
		return '0.0%';
	}
	return `${Math.max(0, Math.min(100, numeric)).toFixed(1)}%`;
};

const formatCount = (value) => {
	const numeric = Number(value);
	if (!Number.isFinite(numeric) || numeric <= 0) {
		return '0';
	}
	return String(Math.round(numeric));
};

const validateDashboardStreamSchema = (schema) => {
	if (!Array.isArray(schema) || schema.length !== DASHBOARD_SAMPLE_SCHEMA.length) {
		throw new Error('仪表板数据格式无效.');
	}
	DASHBOARD_SAMPLE_SCHEMA.forEach((field, index) => {
		if (schema[index] !== field) {
			throw new Error('仪表板数据格式不匹配.');
		}
	});
};

const decodeDashboardStreamSample = (baseTs, row) => {
	const safeBaseTs = Number(baseTs);
	if (!Number.isFinite(safeBaseTs) || safeBaseTs <= 0) {
		throw new Error('仪表板基准时间无效.');
	}
	if (!Array.isArray(row) || row.length !== DASHBOARD_SAMPLE_SCHEMA.length) {
		throw new Error('仪表板采样数据无效.');
	}
	const values = row.map((item) => Number(item));
	if (values.some((item) => !Number.isFinite(item))) {
		throw new Error('仪表板采样数值无效.');
	}
	return {
		time: Math.floor(safeBaseTs + values[0]),
		cpu: Math.max(0, Math.min(100, values[1] / 10)),
		memoryUsed: Math.max(0, values[2]),
		memoryTotal: Math.max(0, values[3]),
		swapUsed: Math.max(0, values[4]),
		swapTotal: Math.max(0, values[5]),
		networkRx: Math.max(0, values[6]),
		networkTx: Math.max(0, values[7]),
		diskRead: Math.max(0, values[8]),
		diskWrite: Math.max(0, values[9]),
		tcpConnections: Math.max(0, values[10]),
		udpConnections: Math.max(0, values[11]),
	};
};

const decodeDashboardStreamSamples = (baseTs, rows) => {
	if (!Array.isArray(rows)) {
		throw new Error('仪表板采样列表无效.');
	}
	return rows.map((row) => decodeDashboardStreamSample(baseTs, row)).filter((sample) => sample.time > 0).sort((a, b) => a.time - b.time);
};

const getGapBreakThresholdSeconds = () => {
	const windowSeconds = Math.max(1, pageState.displayMinutes) * 60;
	return Math.max(3, Math.ceil(windowSeconds / 1000) * 3);
};

const buildTimeSeriesWithGapBreaks = (samples, fieldResolvers) => {
	const times = [];
	const values = fieldResolvers.map(() => []);
	const gapThresholdSeconds = getGapBreakThresholdSeconds();
	samples.forEach((sample, index) => {
		const previousSample = index > 0 ? samples[index - 1] : null;
		if (previousSample && sample.time - previousSample.time > gapThresholdSeconds) {
			const breakTime = Math.floor((previousSample.time + sample.time) / 2);
			if (breakTime > previousSample.time && breakTime < sample.time) {
				times.push(breakTime);
				values.forEach((seriesValues) => seriesValues.push(null));
			}
		}
		times.push(sample.time);
		fieldResolvers.forEach((resolveField, fieldIndex) => {
			values[fieldIndex].push(resolveField(sample));
		});
	});
	return [times, ...values];
};

const buildBrokenSeries = (samples, field) => buildTimeSeriesWithGapBreaks(samples, [(sample) => sample[field]]);

const buildMemorySeries = (samples) => buildTimeSeriesWithGapBreaks(samples, [
	(sample) => sample.memoryUsed,
	(sample) => sample.swapUsed,
]);

const buildDiskSeries = (samples) => buildTimeSeriesWithGapBreaks(samples, [
	(sample) => sample.diskRead,
	(sample) => -sample.diskWrite,
]);

const buildNetworkSeries = (samples) => buildTimeSeriesWithGapBreaks(samples, [
	(sample) => sample.networkRx,
	(sample) => -sample.networkTx,
]);

const buildConnectionSeries = (samples) => buildTimeSeriesWithGapBreaks(samples, [
	(sample) => sample.tcpConnections,
	(sample) => sample.udpConnections,
]);

const getChartSize = (el) => ({
	width: Math.max(1, Math.floor(el.clientWidth || 640)),
	height: Math.max(180, Math.min(260, Math.floor(el.clientHeight || 220))),
});

const grid = {
	show: true,
	stroke: 'rgba(255,255,255,0.08)',
	width: 1,
};

const axisCommon = {
	stroke: '#666',
	font: '11px JetBrains Mono, monospace',
	grid,
	ticks: {
		show: true,
		stroke: 'rgba(255,255,255,0.16)',
		width: 1,
		size: 4,
	},
};

const createChart = (el, options, data) => {
	const UPlotCtor = getUPlotRuntime();
	const size = getChartSize(el);
	return new UPlotCtor({
		width: size.width,
		height: size.height,
		...options,
	}, data, el);
};

const upsertChart = (key, el, options, data) => {
	if (pageState.charts[key]) {
		pageState.charts[key].setData(data);
		if (Array.isArray(options.series)) {
			options.series.forEach((seriesOptions, index) => {
				if (index === 0 || !seriesOptions || typeof pageState.charts[key].setSeries !== 'function') {
					return;
				}
				pageState.charts[key].setSeries(index, seriesOptions, false);
			});
		}
		return;
	}
	pageState.charts[key] = createChart(el, options, data);
};

const pad2 = (value) => String(value).padStart(2, '0');

const formatTimeTick = (value) => {
	const date = new Date(value * 1000);
	return `${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
};

const isLegendValueAvailable = (value) => value !== null && value !== undefined && value !== '' && Number.isFinite(Number(value));

const formatLegendTime = (u, value) => {
	if (pageState.pauseRefreshForCursor && Number.isFinite(pageState.hoverTime) && (!isLegendValueAvailable(value) || !hasChartDataAtTime(u, pageState.hoverTime))) {
		return formatFullTime(pageState.hoverTime);
	}
	const fallbackValue = u && Array.isArray(u.data) && Array.isArray(u.data[0]) ? u.data[0][u.data[0].length - 1] : NaN;
	const rawValue = isLegendValueAvailable(value) ? Number(value) : Number(fallbackValue);
	if (!Number.isFinite(rawValue) || rawValue <= 0) {
		return '--';
	}
	return formatFullTime(rawValue);
};

const formatFullTime = (value) => {
	const date = new Date(value * 1000);
	return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`;
};

const createLegendValueFormatter = (seriesIndex, formatter) => (u, value) => {
	if (pageState.pauseRefreshForCursor && Number.isFinite(pageState.hoverTime) && !hasChartDataAtTime(u, pageState.hoverTime)) {
		return formatter(0);
	}
	const fallbackValue = u && Array.isArray(u.data) && Array.isArray(u.data[seriesIndex]) ? u.data[seriesIndex][u.data[seriesIndex].length - 1] : NaN;
	const rawValue = isLegendValueAvailable(value) ? Number(value) : Number(fallbackValue);
	if (!Number.isFinite(rawValue)) {
		return '--';
	}
	return formatter(rawValue);
};

const formatLegendPercent = (value) => formatPercent(value);
const formatLegendBytes = (value) => formatBytes(value);
const formatLegendBpsAbs = (value) => formatBps(Math.abs(value));

const chooseTimeTickIntervalMinutes = (windowMinutes, chartWidth) => {
	const safeWindowMinutes = Math.max(1, Number(windowMinutes) || DEFAULT_MINUTES);
	const safeChartWidth = Math.max(320, Number(chartWidth) || 640);
	const maxLabels = Math.max(2, Math.floor(safeChartWidth / X_AXIS_TARGET_GRID_SPACE));
	return Math.max(1, Math.ceil(safeWindowMinutes / maxLabels));
};

const formatSparseTimeTicks = (u, values) => {
	const intervalMinutes = chooseTimeTickIntervalMinutes(pageState.displayMinutes, Number(u && u.width) || 0);
	const intervalSeconds = intervalMinutes * 60;
	return values.map((value, index) => {
		const isEdge = index === 0 || index === values.length - 1;
		const roundedValue = Math.round(Number(value) || 0);
		if (!isEdge && roundedValue % intervalSeconds !== 0) {
			return '';
		}
		return formatTimeTick(value);
	});
};

const getMaxOf = (values, fallback = 1) => {
	let max = fallback;
	values.forEach((value) => {
		const numeric = Number(value);
		if (Number.isFinite(numeric) && numeric > max) {
			max = numeric;
		}
	});
	return max;
};

const normalizeDashboardScaleHints = (value) => {
	const hints = value && typeof value === 'object' ? value : {};
	return {
		memoryTotalMax: Math.max(0, Number(hints.memory_total_max) || 0),
		swapTotalMax: Math.max(0, Number(hints.swap_total_max) || 0),
		diskBpsMax: Math.max(0, Number(hints.disk_bps_max) || 0),
		networkBpsMax: Math.max(0, Number(hints.network_bps_max) || 0),
		connectionMax: Math.max(0, Number(hints.connection_max) || 0),
	};
};

const getFullScaleHints = () => pageState.fullLoading && pageState.fullScaleHints ? pageState.fullScaleHints : null;

const getFiniteValues = (values) => values.map((value) => Number(value)).filter((value) => Number.isFinite(value));

const getPaddedRange = (values, fallbackMin, fallbackMax, options = {}) => {
	const finiteValues = getFiniteValues(values);
	if (finiteValues.length === 0) {
		return { min: fallbackMin, max: fallbackMax };
	}
	let min = Math.min(...finiteValues);
	let max = Math.max(...finiteValues);
	if (min === max) {
		const pad = Math.max(Math.abs(max) * DETAIL_SCALE_PADDING_RATIO, 1);
		min -= pad;
		max += pad;
	} else {
		const pad = (max - min) * DETAIL_SCALE_PADDING_RATIO;
		min -= pad;
		max += pad;
	}
	if (Number.isFinite(options.min)) {
		min = Math.max(options.min, min);
	}
	if (Number.isFinite(options.max)) {
		max = Math.min(options.max, max);
	}
	if (max <= min) {
		return { min: fallbackMin, max: fallbackMax };
	}
	return { min, max };
};

const getCpuScaleRange = (samples) => pageState.detailMode.cpu
	? getPaddedRange(samples.map((sample) => sample.cpu), 0, 100, { min: 0, max: 100 })
	: { min: 0, max: 100 };

const getMemoryScaleRanges = (samples, maxPhysicalMemorySize, maxSwapSize) => {
	const hints = getFullScaleHints();
	if (!pageState.detailMode.memory && hints && (hints.memoryTotalMax > 0 || hints.swapTotalMax > 0)) {
		return {
			y: { min: 0, max: hints.memoryTotalMax > 0 ? Math.max(maxPhysicalMemorySize, ceilBytesToDisplayUnit(hints.memoryTotalMax)) : maxPhysicalMemorySize },
			swap: { min: 0, max: hints.swapTotalMax > 0 ? Math.max(maxSwapSize, hints.swapTotalMax) : maxSwapSize },
		};
	}
	if (!pageState.detailMode.memory) {
		return {
			y: { min: 0, max: maxPhysicalMemorySize },
			swap: { min: 0, max: maxSwapSize },
		};
	}
	return {
		y: getPaddedRange(samples.map((sample) => sample.memoryUsed), 0, maxPhysicalMemorySize, { min: 0, max: maxPhysicalMemorySize }),
		swap: getPaddedRange(samples.map((sample) => sample.swapUsed), 0, maxSwapSize, { min: 0, max: maxSwapSize }),
	};
};

const getDiskScaleRange = (diskData, normalMaxDisk) => {
	const hints = getFullScaleHints();
	if (!pageState.detailMode.disk && hints && hints.diskBpsMax > 0) {
		const max = Math.max(1, normalMaxDisk, hints.diskBpsMax * 1.1);
		return { min: -max, max };
	}
	return pageState.detailMode.disk
		? getPaddedRange([...diskData[1], ...diskData[2]], -normalMaxDisk, normalMaxDisk)
		: { min: -normalMaxDisk, max: normalMaxDisk };
};

const getNetworkScaleRange = (networkData, normalMaxNetwork) => {
	const hints = getFullScaleHints();
	if (!pageState.detailMode.network && hints && hints.networkBpsMax > 0) {
		const max = Math.max(1, normalMaxNetwork, hints.networkBpsMax * 1.1);
		return { min: -max, max };
	}
	return pageState.detailMode.network
		? getPaddedRange([...networkData[1], ...networkData[2]], -normalMaxNetwork, normalMaxNetwork)
		: { min: -normalMaxNetwork, max: normalMaxNetwork };
};

const getConnectionScaleRange = (connectionData) => {
	const maxConnection = getMaxOf([...connectionData[1], ...connectionData[2]], 1) * 1.1;
	const hints = getFullScaleHints();
	if (!pageState.detailMode.connection && hints && hints.connectionMax > 0) {
		return { min: 0, max: Math.max(1, maxConnection, hints.connectionMax * 1.1) };
	}
	return { min: 0, max: maxConnection };
};

const reapplyChartScales = (key) => {
	const samples = Array.isArray(pageState.lastSamples) ? pageState.lastSamples : [];
	const chart = pageState.charts[key];
	if (!chart || typeof chart.setScale !== 'function') {
		return;
	}
	const windowRange = getCurrentWindowRange();
	chart.setScale('x', windowRange);
	if (key === 'cpu') {
		chart.setScale('y', getCpuScaleRange(samples));
		return;
	}
	if (key === 'memory') {
		const hints = getFullScaleHints();
		const maxPhysicalMemorySize = hints && hints.memoryTotalMax > 0 ? ceilBytesToDisplayUnit(hints.memoryTotalMax) : ceilBytesToDisplayUnit(getMaxOf(samples.map((sample) => sample.memoryTotal), 1));
		const maxSwapSize = hints && hints.swapTotalMax > 0 ? hints.swapTotalMax : getMaxOf(samples.map((sample) => sample.swapTotal), 1);
		const memoryScaleRanges = getMemoryScaleRanges(samples, maxPhysicalMemorySize, maxSwapSize);
		chart.setScale('y', memoryScaleRanges.y);
		chart.setScale('swap', memoryScaleRanges.swap);
		return;
	}
	if (key === 'disk') {
		const diskData = buildDiskSeries(samples);
		const maxDisk = getMaxOf([...diskData[1], ...diskData[2].map((value) => Math.abs(value))], 1) * 1.1;
		chart.setScale('y', getDiskScaleRange(diskData, maxDisk));
		return;
	}
	if (key === 'network') {
		const networkData = buildNetworkSeries(samples);
		const maxNetwork = getMaxOf([...networkData[1], ...networkData[2].map((value) => Math.abs(value))], 1) * 1.1;
		chart.setScale('y', getNetworkScaleRange(networkData, maxNetwork));
		return;
	}
	if (key === 'connection') {
		chart.setScale('y', getConnectionScaleRange(buildConnectionSeries(samples)));
	}
};

const createScaleLockHooks = (key) => ({
	setSeries: [() => reapplyChartScales(key)],
});

const getCurrentWindowRange = () => {
	const max = Math.floor(Date.now() / 1000);
	const windowSeconds = Math.max(1, pageState.displayMinutes) * 60;
	return {
		min: max - windowSeconds,
		max,
	};
};

const getDashboardChartElements = () => [dom.cpuChart, dom.memoryChart, dom.diskChart, dom.networkChart, dom.connectionChart];

const getAdaptiveTimeAxis = (windowRange) => {
	const chartWidth = Math.max(...getDashboardChartElements().map((chartEl) => chartEl.clientWidth || 0), 640);
	const intervalMinutes = chooseTimeTickIntervalMinutes(pageState.displayMinutes, chartWidth);
	return {
		...axisCommon,
		size: 24,
		gap: 4,
		space: X_AXIS_TARGET_GRID_SPACE,
		incrs: [intervalMinutes * 60],
		values: formatSparseTimeTicks,
	};
};

const buildBaseOptions = (windowRange) => ({
	padding: [null, 20, null, null],
	cursor: {
		show: true,
		x: false,
		y: false,
		points: { show: false },
		drag: { x: false, y: false },
	},
	scales: {
		x: { time: true, min: windowRange.min, max: windowRange.max },
	},
	legend: {
		show: true,
		live: true,
		markers: {
			width: 0,
			fill: (u, seriesIndex) => {
				const series = u.series[seriesIndex];
				if (!series || typeof series.stroke !== 'function') {
					return 'transparent';
				}
				return series.stroke(u, seriesIndex);
			},
		},
	},
	axes: [
		getAdaptiveTimeAxis(windowRange),
	],
});

const renderCharts = (samples) => {
	pageState.lastSamples = Array.isArray(samples) ? samples : [];
	const cpuData = buildBrokenSeries(samples, 'cpu');
	const memoryData = buildMemorySeries(samples);
	const diskData = buildDiskSeries(samples);
	const networkData = buildNetworkSeries(samples);
	const connectionData = buildConnectionSeries(samples);
	const windowRange = getCurrentWindowRange();
	const baseOptions = buildBaseOptions(windowRange);
	const hints = getFullScaleHints();
	const maxPhysicalMemorySize = hints && hints.memoryTotalMax > 0 ? ceilBytesToDisplayUnit(hints.memoryTotalMax) : ceilBytesToDisplayUnit(getMaxOf(samples.map((sample) => sample.memoryTotal), 1));
	const maxSwapSize = hints && hints.swapTotalMax > 0 ? hints.swapTotalMax : getMaxOf(samples.map((sample) => sample.swapTotal), 1);
	const maxDisk = getMaxOf([...diskData[1], ...diskData[2].map((value) => Math.abs(value))], 1) * 1.1;
	const maxNetwork = getMaxOf([...networkData[1], ...networkData[2].map((value) => Math.abs(value))], 1) * 1.1;
	const cpuScaleRange = getCpuScaleRange(samples);
	const memoryScaleRanges = getMemoryScaleRanges(samples, maxPhysicalMemorySize, maxSwapSize);
	const diskScaleRange = getDiskScaleRange(diskData, maxDisk);
	const networkScaleRange = getNetworkScaleRange(networkData, maxNetwork);
	const connectionScaleRange = getConnectionScaleRange(connectionData);

	upsertChart('cpu', dom.cpuChart, {
		...baseOptions,
		hooks: createScaleLockHooks('cpu'),
		scales: { ...baseOptions.scales, y: cpuScaleRange },
		axes: [
			...baseOptions.axes,
			{ ...axisCommon, scale: 'y', size: Y_AXIS_FIXED_SIZE, values: (u, vals) => vals.map((value) => `${value}%`) },
		],
	series: [
		{ value: formatLegendTime },
		{ label: 'CPU', stroke: CHART_BLUE, width: CHART_LINE_WIDTH, fill: CHART_BLUE_FILL, points: HIDDEN_POINTS, value: createLegendValueFormatter(1, formatLegendPercent) },
		],
	}, cpuData);
	pageState.charts.cpu.setScale('x', windowRange);
	pageState.charts.cpu.setScale('y', cpuScaleRange);

	upsertChart('memory', dom.memoryChart, {
		...baseOptions,
		hooks: createScaleLockHooks('memory'),
		scales: {
			...baseOptions.scales,
			y: memoryScaleRanges.y,
			swap: memoryScaleRanges.swap,
		},
		axes: [
			...baseOptions.axes,
			{ ...axisCommon, scale: 'y', size: Y_AXIS_FIXED_SIZE, values: (u, vals) => vals.map(formatBytesCeil) },
		],
		series: [
			{ value: formatLegendTime },
			{ label: 'RAM', scale: 'y', stroke: CHART_BLUE, width: CHART_LINE_WIDTH, fill: CHART_BLUE_FILL, points: HIDDEN_POINTS, value: createLegendValueFormatter(1, formatLegendBytes) },
			{ label: 'SWAP', scale: 'swap', stroke: CHART_YELLOW, width: CHART_LINE_WIDTH, fill: 'transparent', points: HIDDEN_POINTS, value: createLegendValueFormatter(2, formatLegendBytes) },
		],
	}, memoryData);
	pageState.charts.memory.setScale('x', windowRange);
	pageState.charts.memory.setScale('y', memoryScaleRanges.y);
	pageState.charts.memory.setScale('swap', memoryScaleRanges.swap);

	upsertChart('disk', dom.diskChart, {
		...baseOptions,
		hooks: createScaleLockHooks('disk'),
		scales: { ...baseOptions.scales, y: diskScaleRange },
		axes: [
			...baseOptions.axes,
			{ ...axisCommon, scale: 'y', size: Y_AXIS_FIXED_SIZE, values: (u, vals) => vals.map((value) => formatBps(Math.abs(value))) },
		],
		series: [
			{ value: formatLegendTime },
			{ label: 'READ', stroke: CHART_BLUE, width: CHART_LINE_WIDTH, fill: CHART_BLUE_FILL, points: HIDDEN_POINTS, value: createLegendValueFormatter(1, formatLegendBpsAbs) },
			{ label: 'WRITE', stroke: CHART_YELLOW, width: CHART_LINE_WIDTH, fill: 'rgba(244,183,64,0.10)', points: HIDDEN_POINTS, value: createLegendValueFormatter(2, formatLegendBpsAbs) },
		],
	}, diskData);
	pageState.charts.disk.setScale('x', windowRange);
	pageState.charts.disk.setScale('y', diskScaleRange);

	upsertChart('network', dom.networkChart, {
		...baseOptions,
		hooks: createScaleLockHooks('network'),
		scales: { ...baseOptions.scales, y: networkScaleRange },
		axes: [
			...baseOptions.axes,
			{ ...axisCommon, scale: 'y', size: Y_AXIS_FIXED_SIZE, values: (u, vals) => vals.map((value) => formatBps(Math.abs(value))) },
		],
		series: [
			{ value: formatLegendTime },
			{ label: 'RX', stroke: CHART_BLUE, width: CHART_LINE_WIDTH, fill: CHART_BLUE_FILL, points: HIDDEN_POINTS, value: createLegendValueFormatter(1, formatLegendBpsAbs) },
			{ label: 'TX', stroke: CHART_YELLOW, width: CHART_LINE_WIDTH, fill: 'rgba(244,183,64,0.10)', points: HIDDEN_POINTS, value: createLegendValueFormatter(2, formatLegendBpsAbs) },
		],
	}, networkData);
	pageState.charts.network.setScale('x', windowRange);
	pageState.charts.network.setScale('y', networkScaleRange);

	upsertChart('connection', dom.connectionChart, {
		...baseOptions,
		hooks: createScaleLockHooks('connection'),
		scales: { ...baseOptions.scales, y: connectionScaleRange },
		axes: [
			...baseOptions.axes,
			{ ...axisCommon, scale: 'y', size: Y_AXIS_FIXED_SIZE, values: (u, vals) => vals.map(formatCount) },
		],
		series: [
			{ value: formatLegendTime },
			{ label: 'TCP', stroke: CHART_BLUE, width: CHART_LINE_WIDTH, fill: CHART_BLUE_FILL, points: HIDDEN_POINTS, value: createLegendValueFormatter(1, formatCount) },
			{ label: 'UDP', stroke: CHART_YELLOW, width: CHART_LINE_WIDTH, fill: 'transparent', points: HIDDEN_POINTS, value: createLegendValueFormatter(2, formatCount) },
		],
	}, connectionData);
	pageState.charts.connection.setScale('x', windowRange);
	pageState.charts.connection.setScale('y', connectionScaleRange);
};

const updateInterfaceOptions = (interfaces) => {
	if (!canUseDashboardDeviceFilters()) {
		const changed = pageState.selectedInterface !== '';
		pageState.selectedInterface = '';
		if (dom.interfaceSelect) {
			dom.interfaceSelect.textContent = '';
		}
		return changed;
	}
	if (!Array.isArray(interfaces)) {
		return false;
	}
	const values = interfaces.map((item) => String(item || '').trim()).filter(Boolean);
	const existing = new Set(Array.from(dom.interfaceSelect.options).map((option) => option.value).filter((value) => value !== SELECT_SEPARATOR_VALUE));
	const next = new Set(['', ...values]);
	let changed = existing.size !== next.size;
	if (!changed) {
		next.forEach((value) => {
			if (!existing.has(value)) changed = true;
		});
	}
	if (!changed) {
		return false;
	}
	const previousSelectedInterface = pageState.selectedInterface;
	dom.interfaceSelect.textContent = '';
	const allOption = document.createElement('option');
	allOption.value = '';
	allOption.textContent = 'ALL';
	dom.interfaceSelect.appendChild(allOption);
	if (values.length > 0) {
		const separatorOption = document.createElement('option');
		separatorOption.value = SELECT_SEPARATOR_VALUE;
		separatorOption.textContent = '────────';
		separatorOption.disabled = true;
		dom.interfaceSelect.appendChild(separatorOption);
	}
	values.forEach((value) => {
		const option = document.createElement('option');
		option.value = value;
		option.textContent = value;
		dom.interfaceSelect.appendChild(option);
	});
	if (next.has(pageState.selectedInterface)) {
		dom.interfaceSelect.value = pageState.selectedInterface;
	} else {
		pageState.selectedInterface = '';
		dom.interfaceSelect.value = '';
	}
	if (previousSelectedInterface !== pageState.selectedInterface) {
		storeText(STORAGE_INTERFACE_KEY, pageState.selectedInterface);
	}
	return previousSelectedInterface !== pageState.selectedInterface;
};

const updateDiskOptions = (disks) => {
	if (!canUseDashboardDeviceFilters()) {
		const changed = pageState.selectedDisk !== '';
		pageState.selectedDisk = '';
		if (dom.diskSelect) {
			dom.diskSelect.textContent = '';
		}
		return changed;
	}
	if (!Array.isArray(disks)) {
		return false;
	}
	const values = disks.map((item) => String(item || '').trim()).filter(Boolean);
	const existing = new Set(Array.from(dom.diskSelect.options).map((option) => option.value).filter((value) => value !== SELECT_SEPARATOR_VALUE));
	const next = new Set(['', ...values]);
	let changed = existing.size !== next.size;
	if (!changed) {
		next.forEach((value) => {
			if (!existing.has(value)) changed = true;
		});
	}
	if (!changed) {
		return false;
	}
	const previousSelectedDisk = pageState.selectedDisk;
	dom.diskSelect.textContent = '';
	const allOption = document.createElement('option');
	allOption.value = '';
	allOption.textContent = 'ALL';
	dom.diskSelect.appendChild(allOption);
	if (values.length > 0) {
		const separatorOption = document.createElement('option');
		separatorOption.value = SELECT_SEPARATOR_VALUE;
		separatorOption.textContent = '────────';
		separatorOption.disabled = true;
		dom.diskSelect.appendChild(separatorOption);
	}
	values.forEach((value) => {
		const option = document.createElement('option');
		option.value = value;
		option.textContent = value;
		dom.diskSelect.appendChild(option);
	});
	if (next.has(pageState.selectedDisk)) {
		dom.diskSelect.value = pageState.selectedDisk;
	} else {
		pageState.selectedDisk = '';
		dom.diskSelect.value = '';
	}
	if (previousSelectedDisk !== pageState.selectedDisk) {
		storeText(STORAGE_DISK_KEY, pageState.selectedDisk);
	}
	return previousSelectedDisk !== pageState.selectedDisk;
};

const setError = (message) => {
	const text = String(message || '').trim();
	if (!text) {
		dom.error.classList.add('hidden');
		dom.error.textContent = '';
		return;
	}
	dom.error.textContent = text;
	dom.error.classList.remove('hidden');
};

const renderDashboardSamples = (force = false) => {
	if (pageState.pauseRefreshForCursor && !force) {
		pageState.pendingRender = true;
		return;
	}
	pageState.pendingRender = false;
	renderCharts(pageState.lastSamples);
};

const trimDashboardSamples = () => {
	const windowSeconds = Math.max(1, pageState.displayMinutes) * 60;
	const cutoff = Math.floor(Date.now() / 1000) - windowSeconds;
	pageState.lastSamples = pageState.lastSamples.filter((sample) => sample.time >= cutoff);
};

const parseSSEPayload = (event) => parseSSEJsonData(event, '仪表板推送数据解析失败');

const handleDashboardFull = (payload) => {
	if (!payload || typeof payload !== 'object') {
		throw new Error('仪表板全量数据无效.');
	}
	const displayMinutes = parseSelectedMinutesFromPayload(payload);
	validateDashboardStreamSchema(payload.sample_schema);
	const seq = Number(payload.seq);
	const baseTs = Number(payload.base_ts);
	if (!Number.isFinite(seq) || seq < 0 || !Number.isFinite(baseTs) || baseTs <= 0) {
		throw new Error('仪表板全量数据序号无效.');
	}
	const samples = decodeDashboardStreamSamples(baseTs, payload.samples);
	pageState.fullLoading = false;
	pageState.fullScaleHints = null;
	pageState.preserveSamplesOnNextFull = false;
	pageState.displayMinutes = displayMinutes;
	pageState.lastSeq = seq;
	pageState.lastOptionsSeq = 0;
	pageState.baseTs = baseTs;
	pageState.reconnectAttempt = 0;
	pageState.lastSamples = samples;
	const interfaceChanged = updateInterfaceOptions(payload.interfaces);
	const diskChanged = updateDiskOptions(payload.disks);
	if (interfaceChanged || diskChanged) {
		restartDashboardStream();
		return;
	}
	trimDashboardSamples();
	renderDashboardSamples();
	setError('');
};

const handleDashboardFullMeta = (payload) => {
	if (!payload || typeof payload !== 'object') {
		throw new Error('仪表板全量元数据无效.');
	}
	const displayMinutes = parseSelectedMinutesFromPayload(payload);
	validateDashboardStreamSchema(payload.sample_schema);
	const seq = Number(payload.seq);
	const baseTs = Number(payload.base_ts);
	if (!Number.isFinite(seq) || seq < 0 || !Number.isFinite(baseTs) || baseTs <= 0) {
		throw new Error('仪表板全量元数据序号无效.');
	}
	const scaleHints = normalizeDashboardScaleHints(payload.scale_hints);
	pageState.displayMinutes = displayMinutes;
	pageState.lastSeq = seq;
	pageState.lastOptionsSeq = 0;
	pageState.baseTs = baseTs;
	pageState.fullLoading = true;
	pageState.fullScaleHints = scaleHints;
	if (!pageState.preserveSamplesOnNextFull) {
		pageState.lastSamples = [];
	}
	pageState.preserveSamplesOnNextFull = false;
	pageState.reconnectAttempt = 0;
	const interfaceChanged = updateInterfaceOptions(payload.interfaces);
	const diskChanged = updateDiskOptions(payload.disks);
	if (interfaceChanged || diskChanged) {
		restartDashboardStream();
		return;
	}
	trimDashboardSamples();
	renderDashboardSamples(true);
	setError('');
};

const handleDashboardFullSamples = (payload) => {
	if (!payload || typeof payload !== 'object') {
		throw new Error('仪表板全量采样数据无效.');
	}
	const seq = Number(payload.seq);
	const baseTs = Number(payload.base_ts);
	if (!Number.isFinite(seq) || seq < 0 || !Number.isFinite(baseTs) || baseTs <= 0) {
		throw new Error('仪表板全量采样序号无效.');
	}
	if (!pageState.fullLoading || seq !== pageState.lastSeq || baseTs !== pageState.baseTs) {
		throw new Error('仪表板全量采样数据与元数据不匹配.');
	}
	pageState.lastSamples = decodeDashboardStreamSamples(baseTs, payload.samples);
	pageState.lastSeq = seq;
	pageState.baseTs = baseTs;
	pageState.fullLoading = false;
	trimDashboardSamples();
	renderDashboardSamples(true);
	setError('');
};

const handleDashboardAppend = (payload) => {
	if (!payload || typeof payload !== 'object') {
		throw new Error('仪表板增量数据无效.');
	}
	if (pageState.fullLoading) {
		throw new Error('仪表板全量数据尚未加载完成.');
	}
	const seq = Number(payload.seq);
	const baseTs = Number(payload.base_ts);
	if (!Number.isFinite(seq) || seq <= 0 || !Number.isFinite(baseTs) || baseTs <= 0) {
		throw new Error('仪表板增量数据序号无效.');
	}
	if (seq <= pageState.lastSeq) {
		return;
	}
	if (pageState.lastSeq > 0 && seq !== pageState.lastSeq + 1) {
		throw new Error('仪表板增量数据不连续.');
	}
	const sample = decodeDashboardStreamSample(baseTs, payload.sample);
	pageState.lastSeq = seq;
	pageState.baseTs = baseTs;
	const lastSample = pageState.lastSamples[pageState.lastSamples.length - 1];
	if (lastSample && lastSample.time === sample.time) {
		pageState.lastSamples[pageState.lastSamples.length - 1] = sample;
	} else if (!lastSample || sample.time > lastSample.time) {
		pageState.lastSamples.push(sample);
	} else {
		pageState.lastSamples.push(sample);
		pageState.lastSamples.sort((a, b) => a.time - b.time);
	}
	trimDashboardSamples();
	renderDashboardSamples();
	setError('');
};

const handleDashboardOptions = (payload) => {
	if (!payload || typeof payload !== 'object') {
		throw new Error('仪表板选项数据无效.');
	}
	const seq = Number(payload.seq);
	if (Number.isFinite(seq) && seq > pageState.lastOptionsSeq) {
		pageState.lastOptionsSeq = seq;
	}
	const interfaceChanged = updateInterfaceOptions(payload.interfaces);
	const diskChanged = updateDiskOptions(payload.disks);
	if (interfaceChanged || diskChanged) {
		restartDashboardStream();
	}
};

const handleDashboardDisabled = (payload) => {
	const data = payload && typeof payload === 'object' ? payload : {};
	const displayMinutes = parseSelectedMinutesFromPayload(data);
	const interfaces = Array.isArray(data.interfaces) ? data.interfaces : [];
	const disks = Array.isArray(data.disks) ? data.disks : [];
	pageState.displayMinutes = displayMinutes;
	pageState.lastSeq = 0;
	pageState.lastOptionsSeq = 0;
	pageState.baseTs = 0;
	pageState.fullLoading = false;
	pageState.fullScaleHints = null;
	pageState.preserveSamplesOnNextFull = false;
	pageState.publicUnauthorizedRetryUsed = false;
	pageState.lastSamples = [];
	const interfaceChanged = updateInterfaceOptions(interfaces);
	const diskChanged = updateDiskOptions(disks);
	if (interfaceChanged || diskChanged) {
		restartDashboardStream();
		return;
	}
	renderDashboardSamples(true);
	setError('仪表板统计已关闭.');
};

const stopDashboardStream = () => {
	if (pageState.reconnectTimer) {
		window.clearTimeout(pageState.reconnectTimer);
		pageState.reconnectTimer = 0;
	}
	if (pageState.stream) {
		pageState.stream.controller.abort();
		pageState.stream = null;
	}
};

const resetDashboardStreamState = () => {
	pageState.lastSeq = 0;
	pageState.lastOptionsSeq = 0;
	pageState.baseTs = 0;
	pageState.fullLoading = false;
	pageState.fullScaleHints = null;
	pageState.preserveSamplesOnNextFull = false;
	pageState.publicUnauthorizedRetryUsed = false;
	pageState.lastSamples = [];
	pageState.pendingRender = false;
	hideLinkedCrosshairs();
	renderDashboardSamples(true);
};

const scheduleDashboardReconnect = (delay = null) => {
	if (!pageState.active || pageState.reconnectTimer) {
		return;
	}
	pageState.reconnectAttempt += 1;
	const reconnectDelay = delay === null ? getReconnectDelay(pageState.reconnectAttempt) : Math.max(0, Number(delay) || 0);
	pageState.reconnectTimer = window.setTimeout(() => {
		pageState.reconnectTimer = 0;
		connectDashboardStream();
	}, reconnectDelay);
};

const restartDashboardStream = () => {
	stopDashboardStream();
	resetDashboardStreamState();
	if (pageState.active) {
		connectDashboardStream();
	}
};

const handleDashboardStreamEvent = (event, handler, errorText, reconnectNow) => {
	try {
		handler(parseSSEPayload(event));
	} catch (error) {
		console.error(`[Dashboard] ${errorText}:`, error);
		setError(error.message || String(error));
		if (reconnectNow) {
			stopDashboardStream();
			scheduleDashboardReconnect(0);
		}
	}
};

async function connectDashboardStream() {
	if (!pageState.active || !isSystemDashboardPageActive() || pageState.stream) {
		return;
	}
	const controller = new AbortController();
	const stream = { controller };
	pageState.stream = stream;
	try {
		const publicDashboardMode = isPublicDashboardMode();
		const res = await openDashboardEventStream({
			minutes: pageState.requestedMinutes,
			nic: pageState.selectedInterface,
			disk: pageState.selectedDisk,
		}, {
			signal: controller.signal,
			suppressUnauthorizedEvent: publicDashboardMode,
		});
		pageState.reconnectAttempt = 0;
		pageState.publicUnauthorizedRetryUsed = false;
		await readSSEStream(res, {
			auth_required() {
				stopDashboardStream();
				dispatchUnauthorized();
			},
			dashboard_full: (event) => handleDashboardStreamEvent(event, handleDashboardFull, '处理全量推送失败', true),
			dashboard_full_meta: (event) => handleDashboardStreamEvent(event, handleDashboardFullMeta, '处理全量元数据推送失败', true),
			dashboard_full_samples: (event) => handleDashboardStreamEvent(event, handleDashboardFullSamples, '处理全量采样推送失败', true),
			dashboard_append: (event) => handleDashboardStreamEvent(event, handleDashboardAppend, '处理增量推送失败', true),
			dashboard_options: (event) => handleDashboardStreamEvent(event, handleDashboardOptions, '处理选项推送失败', false),
			dashboard_disabled: (event) => handleDashboardStreamEvent(event, handleDashboardDisabled, '处理关闭状态推送失败', false),
			dashboard_error(event) {
				try {
					const payload = parseSSEPayload(event);
					const message = payload && typeof payload === 'object' ? String(payload.message || '').trim() : '';
					setError(message || '仪表板推送发生错误.');
				} catch (error) {
					console.error('[Dashboard] 处理错误推送失败:', error);
					setError(error.message || String(error));
				}
			},
		});
	} catch (error) {
		if (error.name !== 'AbortError') {
			console.error('[Dashboard] 仪表板推送连接失败:', error);
			if (error.status === 401) {
				if (isPublicDashboardMode()) {
					if (!pageState.publicUnauthorizedRetryUsed) {
						pageState.publicUnauthorizedRetryUsed = true;
						stream.reconnectDelay = 0;
					} else {
						stream.reconnect = false;
						setError('仪表板访问状态已更新, 请刷新页面后重试.');
					}
				}
			} else {
				setError(error.message || String(error));
			}
		}
	} finally {
		if (pageState.stream === stream) {
			pageState.stream = null;
			if (stream.reconnect !== false && pageState.active && isSystemDashboardPageActive()) {
				const reconnectDelay = Object.prototype.hasOwnProperty.call(stream, 'reconnectDelay') ? stream.reconnectDelay : null;
				scheduleDashboardReconnect(reconnectDelay);
			}
		}
	}
}

const resizeCharts = () => {
	if (!isSystemDashboardPageActive()) {
		return;
	}
	Object.entries(pageState.charts).forEach(([key, chart]) => {
		if (!chart) {
			return;
		}
		const el = getChartElementByKey(key);
		if (!el) {
			return;
		}
		const size = getChartSize(el);
		chart.setSize(size);
	});
};

const getChartElementByKey = (key) => {
	if (key === 'cpu') {
		return dom.cpuChart;
	}
	if (key === 'memory') {
		return dom.memoryChart;
	}
	if (key === 'disk') {
		return dom.diskChart;
	}
	if (key === 'network') {
		return dom.networkChart;
	}
	if (key === 'connection') {
		return dom.connectionChart;
	}
	return null;
};

const getChartElements = getDashboardChartElements;

const getChartByElement = (chartEl) => {
	if (chartEl === dom.cpuChart) {
		return pageState.charts.cpu;
	}
	if (chartEl === dom.memoryChart) {
		return pageState.charts.memory;
	}
	if (chartEl === dom.diskChart) {
		return pageState.charts.disk;
	}
	if (chartEl === dom.networkChart) {
		return pageState.charts.network;
	}
	if (chartEl === dom.connectionChart) {
		return pageState.charts.connection;
	}
	return null;
};

const getChartValueFormatterByElement = (chartEl) => {
	if (chartEl === dom.cpuChart) {
		return formatPercent;
	}
	if (chartEl === dom.memoryChart) {
		return formatBytes;
	}
	if (chartEl === dom.diskChart) {
		return (value) => formatBps(Math.abs(value));
	}
	if (chartEl === dom.networkChart) {
		return (value) => formatBps(Math.abs(value));
	}
	if (chartEl === dom.connectionChart) {
		return formatCount;
	}
	return (value) => String(value);
};

const getChartYValueAtRatio = (chart, yRatio) => {
	if (!chart || !chart.scales || !chart.scales.y) {
		return NaN;
	}
	const min = Number(chart.scales.y.min);
	const max = Number(chart.scales.y.max);
	if (!Number.isFinite(min) || !Number.isFinite(max) || max <= min) {
		return NaN;
	}
	return max - (max - min) * yRatio;
};

const getChartList = () => [pageState.charts.cpu, pageState.charts.memory, pageState.charts.disk, pageState.charts.network, pageState.charts.connection].filter((chart) => !!chart);

const getChartKeyByElement = (chartEl) => {
	if (chartEl === dom.cpuChart) {
		return 'cpu';
	}
	if (chartEl === dom.memoryChart) {
		return 'memory';
	}
	if (chartEl === dom.diskChart) {
		return 'disk';
	}
	if (chartEl === dom.networkChart) {
		return 'network';
	}
	if (chartEl === dom.connectionChart) {
		return 'connection';
	}
	return '';
};

const toggleDetailMode = (chartEl) => {
	const key = getChartKeyByElement(chartEl);
	if (!key) {
		return;
	}
	const nextDetailMode = !pageState.detailMode[key];
	setAllDetailModes(nextDetailMode);
	storeDetailMode(nextDetailMode);
	if (Array.isArray(pageState.lastSamples)) {
		renderCharts(pageState.lastSamples);
	}
};

const findClosestTimeIndex = (times, targetTime) => {
	if (!Array.isArray(times) || times.length === 0 || !Number.isFinite(targetTime)) {
		return null;
	}
	let bestIndex = 0;
	let bestDistance = Math.abs(Number(times[0]) - targetTime);
	for (let index = 1; index < times.length; index += 1) {
		const distance = Math.abs(Number(times[index]) - targetTime);
		if (distance < bestDistance) {
			bestDistance = distance;
			bestIndex = index;
		}
	}
	return bestIndex;
};

const isChartValueAvailableAtIndex = (chart, index) => {
	if (!chart || !Array.isArray(chart.data) || !Number.isInteger(index) || index < 0) {
		return false;
	}
	for (let seriesIndex = 1; seriesIndex < chart.data.length; seriesIndex += 1) {
		const seriesValues = chart.data[seriesIndex];
		if (Array.isArray(seriesValues) && isLegendValueAvailable(seriesValues[index])) {
			return true;
		}
	}
	return false;
};

const findTimeInsertIndex = (times, targetTime) => {
	let low = 0;
	let high = times.length;
	while (low < high) {
		const mid = Math.floor((low + high) / 2);
		const time = Number(times[mid]);
		if (Number.isFinite(time) && time < targetTime) {
			low = mid + 1;
		} else {
			high = mid;
		}
	}
	return low;
};

const hasChartDataAtTime = (chart, targetTime) => {
	const times = chart && Array.isArray(chart.data) ? chart.data[0] : null;
	if (!Array.isArray(times) || times.length === 0 || !Number.isFinite(targetTime)) {
		return false;
	}
	const first = Number(times[0]);
	const last = Number(times[times.length - 1]);
	if (!Number.isFinite(first) || !Number.isFinite(last) || targetTime < first || targetTime > last) {
		return false;
	}
	const nextIndex = findTimeInsertIndex(times, targetTime);
	const nextTime = Number(times[nextIndex]);
	if (Number.isFinite(nextTime) && nextTime === targetTime) {
		return isChartValueAvailableAtIndex(chart, nextIndex);
	}
	const previousIndex = nextIndex - 1;
	if (previousIndex < 0 || nextIndex >= times.length) {
		return false;
	}
	return isChartValueAvailableAtIndex(chart, previousIndex) && isChartValueAvailableAtIndex(chart, nextIndex);
};

const getTimeAtChartRatio = (chart, xRatio) => {
	if (!chart || !chart.scales || !chart.scales.x) {
		return NaN;
	}
	const min = Number(chart.scales.x.min);
	const max = Number(chart.scales.x.max);
	if (!Number.isFinite(min) || !Number.isFinite(max) || max <= min) {
		return NaN;
	}
	return min + (max - min) * xRatio;
};

const applyLinkedChartLegends = (targetTime) => {
	pageState.hoverTime = targetTime;
	pageState.legendEmptyHover = getChartList().every((chart) => !hasChartDataAtTime(chart, targetTime));
	getChartList().forEach((chart) => {
		if (typeof chart.setLegend !== 'function' || !Array.isArray(chart.data) || !Array.isArray(chart.data[0])) {
			return;
		}
		if (!hasChartDataAtTime(chart, targetTime)) {
			chart.setLegend({ idx: null }, false);
			return;
		}
		const index = findClosestTimeIndex(chart.data[0], targetTime);
		if (index === null) {
			chart.setLegend({ idx: null }, false);
			return;
		}
		chart.setLegend({ idx: index }, false);
	});
};

const syncLinkedChartLegends = (sourceChartEl, xRatio) => {
	const sourceChart = getChartByElement(sourceChartEl);
	const targetTime = getTimeAtChartRatio(sourceChart, xRatio);
	if (!Number.isFinite(targetTime)) {
		return;
	}
	applyLinkedChartLegends(targetTime);
	const syncFrame = pageState.legendSyncFrame + 1;
	pageState.legendSyncFrame = syncFrame;
	requestAnimationFrame(() => {
		if (pageState.legendSyncFrame !== syncFrame || !pageState.pauseRefreshForCursor) {
			return;
		}
		applyLinkedChartLegends(targetTime);
	});
};

const resetLinkedChartLegends = () => {
	pageState.legendSyncFrame += 1;
	pageState.legendEmptyHover = false;
	pageState.hoverTime = NaN;
	getChartList().forEach((chart) => {
		if (typeof chart.setLegend === 'function') {
			chart.setLegend({ idx: null }, false);
		}
	});
};

const getPlotRect = (chartEl) => {
	const plot = chartEl.querySelector('.u-over');
	if (!plot) {
		return chartEl.getBoundingClientRect();
	}
	return plot.getBoundingClientRect();
};

const ensureCrosshair = (chartEl) => {
	if (pageState.crosshairs.has(chartEl)) {
		return pageState.crosshairs.get(chartEl);
	}
	const root = document.createElement('div');
	root.className = 'dashboard-crosshair hidden';
	const vertical = document.createElement('span');
	vertical.className = 'dashboard-crosshair-line dashboard-crosshair-vertical';
	const horizontal = document.createElement('span');
	horizontal.className = 'dashboard-crosshair-line dashboard-crosshair-horizontal';
	const valueLabel = document.createElement('span');
	valueLabel.className = 'dashboard-crosshair-value';
	root.appendChild(vertical);
	root.appendChild(horizontal);
	root.appendChild(valueLabel);
	chartEl.appendChild(root);
	const crosshair = { root, vertical, horizontal, valueLabel };
	pageState.crosshairs.set(chartEl, crosshair);
	return crosshair;
};

const hideLinkedCrosshairs = () => {
	pageState.crosshairs.forEach((crosshair) => {
		crosshair.root.classList.add('hidden');
	});
	resetLinkedChartLegends();
};

const isPointInsideRect = (clientX, clientY, rect) => clientX >= rect.left && clientX <= rect.right && clientY >= rect.top && clientY <= rect.bottom;

const isPointInsideAnyChartPlot = (clientX, clientY) => {
	return getChartElements().some((chartEl) => {
		if (!chartEl) {
			return false;
		}
		return isPointInsideRect(clientX, clientY, getPlotRect(chartEl));
	});
};

const pauseRefreshForChartCursor = () => {
	pageState.pauseRefreshForCursor = true;
};

const syncRefreshPauseWithChartCursor = (chartEl, event) => {
	if (isEventInsideChartPlot(chartEl, event)) {
		pauseRefreshForChartCursor();
		return;
	}
	resumeRefreshAfterChartCursor(event);
};

const resumeRefreshAfterChartCursor = (event) => {
	if (event && isPointInsideAnyChartPlot(event.clientX, event.clientY)) {
		return;
	}
	resumeRefreshAndHideCrosshairs();
};

const resumeRefreshAndHideCrosshairs = (renderPending = true) => {
	pageState.pauseRefreshForCursor = false;
	hideLinkedCrosshairs();
	if (renderPending && pageState.pendingRender) {
		renderDashboardSamples(true);
	}
};

const resumeRefreshAfterBrowserPointerExit = (event) => {
	if (!pageState.pauseRefreshForCursor) {
		return;
	}
	if (event && event.relatedTarget) {
		return;
	}
	resumeRefreshAndHideCrosshairs();
};

const resumeRefreshAfterBrowserInactive = () => {
	if (!pageState.pauseRefreshForCursor) {
		return;
	}
	resumeRefreshAndHideCrosshairs();
};

const resumeRefreshAfterVisibilityHidden = () => {
	if (document.visibilityState !== 'hidden') {
		return;
	}
	resumeRefreshAfterBrowserInactive();
};

const moveLinkedCrosshairs = (sourceChartEl, event) => {
	if (!sourceChartEl || !event) {
		hideLinkedCrosshairs();
		return;
	}
	const sourcePlotRect = getPlotRect(sourceChartEl);
	if (sourcePlotRect.width <= 0 || sourcePlotRect.height <= 0) {
		resumeRefreshAfterChartCursor(event);
		hideLinkedCrosshairs();
		return;
	}
	if (event.clientX < sourcePlotRect.left || event.clientX > sourcePlotRect.right || event.clientY < sourcePlotRect.top || event.clientY > sourcePlotRect.bottom) {
		resumeRefreshAfterChartCursor(event);
		hideLinkedCrosshairs();
		return;
	}
	pauseRefreshForChartCursor();
	const xRatio = Math.max(0, Math.min(1, (event.clientX - sourcePlotRect.left) / sourcePlotRect.width));
	const yRatio = Math.max(0, Math.min(1, (event.clientY - sourcePlotRect.top) / sourcePlotRect.height));
	syncLinkedChartLegends(sourceChartEl, xRatio);
	getChartElements().forEach((chartEl) => {
		if (!chartEl) {
			return;
		}
		const chartRect = chartEl.getBoundingClientRect();
		const plotRect = getPlotRect(chartEl);
		const x = plotRect.left - chartRect.left + plotRect.width * xRatio;
		const y = plotRect.top - chartRect.top + plotRect.height * yRatio;
		const chart = getChartByElement(chartEl);
		const yValue = getChartYValueAtRatio(chart, yRatio);
		const formatYValue = getChartValueFormatterByElement(chartEl);
		const crosshair = ensureCrosshair(chartEl);
		crosshair.root.classList.remove('hidden');
		crosshair.valueLabel.textContent = Number.isFinite(yValue) ? formatYValue(yValue) : '--';
		const labelWidth = crosshair.valueLabel.offsetWidth || 0;
		const labelHeight = crosshair.valueLabel.offsetHeight || 0;
		crosshair.vertical.style.left = `${x}px`;
		crosshair.vertical.style.top = `${plotRect.top - chartRect.top}px`;
		crosshair.vertical.style.height = `${plotRect.height}px`;
		crosshair.horizontal.style.left = `${plotRect.left - chartRect.left}px`;
		crosshair.horizontal.style.top = `${y}px`;
		crosshair.horizontal.style.width = `${plotRect.width}px`;
		crosshair.valueLabel.style.left = `${Math.round(plotRect.left - chartRect.left - labelWidth - 6)}px`;
		crosshair.valueLabel.style.top = `${Math.round(y - labelHeight / 2)}px`;
	});
};

const isEventInsideChartPlot = (chartEl, event) => {
	if (!chartEl || !event) {
		return false;
	}
	return isPointInsideRect(event.clientX, event.clientY, getPlotRect(chartEl));
};

const showLinkedCrosshairsFromChartClick = (chartEl, event) => {
	if (!isEventInsideChartPlot(chartEl, event)) {
		resumeRefreshAndHideCrosshairs();
		return;
	}
	pageState.pauseRefreshForCursor = true;
	moveLinkedCrosshairs(chartEl, event);
};

const hideLinkedCrosshairsFromNonPlotClick = (event) => {
	if (!event || isPointInsideAnyChartPlot(event.clientX, event.clientY)) {
		return;
	}
	resumeRefreshAndHideCrosshairs();
};

const toggleDetailModeFromChartDoubleClick = (chartEl, event) => {
	if (!isEventInsideChartPlot(chartEl, event)) {
		return;
	}
	event.preventDefault();
	event.stopPropagation();
	toggleDetailMode(chartEl);
};

const toggleLegendSeriesFromRowClick = (chartEl, event) => {
	const row = event.target.closest('.u-series');
	if (!row || !chartEl.contains(row)) {
		return;
	}
	const rows = Array.from(row.parentElement ? row.parentElement.querySelectorAll('.u-series') : []);
	const seriesIndex = rows.indexOf(row);
	if (seriesIndex < 0) {
		return;
	}
	event.preventDefault();
	event.stopPropagation();
	if (seriesIndex === 0) {
		toggleDetailMode(chartEl);
		return;
	}
	const chart = getChartByElement(chartEl);
	if (!chart || !chart.series || !chart.series[seriesIndex] || typeof chart.setSeries !== 'function') {
		return;
	}
	chart.setSeries(seriesIndex, { show: !chart.series[seriesIndex].show });
};

const bindEvents = () => {
	dom.pageTabs.forEach((tab) => {
		tab.addEventListener('click', () => setDashboardPage(tab.dataset.page));
	});
	dom.minutesInput.addEventListener('change', () => {
		pageState.requestedMinutes = clampMinutes(dom.minutesInput.value);
		dom.minutesInput.value = String(pageState.requestedMinutes);
		storeMinutes(pageState.requestedMinutes);
		restartDashboardStream();
	});
	dom.interfaceSelect.addEventListener('change', () => {
		if (!canUseDashboardDeviceFilters()) {
			return;
		}
		pageState.selectedInterface = String(dom.interfaceSelect.value || '').trim();
		storeText(STORAGE_INTERFACE_KEY, pageState.selectedInterface);
		restartDashboardStream();
	});
	dom.diskSelect.addEventListener('change', () => {
		if (!canUseDashboardDeviceFilters()) {
			return;
		}
		pageState.selectedDisk = String(dom.diskSelect.value || '').trim();
		storeText(STORAGE_DISK_KEY, pageState.selectedDisk);
		restartDashboardStream();
	});
	getChartElements().forEach((chartEl) => {
		chartEl.addEventListener('click', (event) => showLinkedCrosshairsFromChartClick(chartEl, event));
		chartEl.addEventListener('click', (event) => toggleLegendSeriesFromRowClick(chartEl, event));
		chartEl.addEventListener('dblclick', (event) => toggleDetailModeFromChartDoubleClick(chartEl, event), true);
		chartEl.addEventListener('pointermove', (event) => moveLinkedCrosshairs(chartEl, event));
		chartEl.addEventListener('pointerenter', (event) => syncRefreshPauseWithChartCursor(chartEl, event));
		chartEl.addEventListener('pointerleave', resumeRefreshAfterChartCursor);
	});
	document.addEventListener('click', hideLinkedCrosshairsFromNonPlotClick);
	document.documentElement.addEventListener('mouseleave', resumeRefreshAfterBrowserPointerExit);
	window.addEventListener('blur', resumeRefreshAfterBrowserInactive);
	document.addEventListener('visibilitychange', resumeRefreshAfterVisibilityHidden);
	pageState.resizeObserver = new ResizeObserver(() => resizeCharts());
	pageState.resizeObserver.observe(dom.section);
};

const showPage = () => {
	stopDashboardStream();
	resetDashboardStreamState();
	syncDashboardDeviceFilterVisibility();
	syncDashboardPage();
	pageState.active = true;
	dom.section.classList.add('active');
	if (isSystemDashboardPageActive()) {
		connectDashboardStream();
	}
};

const hidePage = () => {
	pageState.active = false;
	dom.section.classList.remove('active');
	stopDashboardStream();
	resumeRefreshAndHideCrosshairs(false);
};

export const bootDashboardPage = () => {
	pageState.requestedMinutes = readStoredMinutes();
	pageState.displayMinutes = pageState.requestedMinutes;
	setAllDetailModes(readStoredDetailMode());
	dom.minutesInput.value = String(pageState.requestedMinutes);
	syncDashboardPage();
	syncDashboardDeviceFilterVisibility();
	updateInterfaceOptions([]);
	updateDiskOptions([]);
	if (canUseDashboardDeviceFilters()) {
		pageState.selectedInterface = readStoredText(STORAGE_INTERFACE_KEY);
		pageState.selectedDisk = readStoredText(STORAGE_DISK_KEY);
	}
	bindEvents();
	return {
		showPage,
		hidePage,
		refresh: restartDashboardStream,
	};
};
