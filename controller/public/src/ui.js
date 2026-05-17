
console.log(String.raw`%c
 ______                                                            __     
/\__  _\                                                          /\ \    
\/_/\ \/    _____     ____     ____   ____    ____ ___     ____   \_\ \   
   \ \ \   /\  __ \  / __ \   / ___\ / __ \  /  __  __ \  / __ \  / __ \  
    \_\ \__\ \ \/\ \/\ \/\ \_/\ \__//\ \/\ \_/\ \/\ \/\ \/\ \/\ \/\ \/\ \ 
    /\_____\\ \  __/\ \__/ \_\ \____\ \__/ \_\ \_\ \_\ \_\ \____/\ \_____\
    \/_____/ \ \ \/  \/__/\/_/\/____/\/__/\/_/\/_/\/_/\/_/\/___/  \/____ /
              \ \_\                                                       
               \/_/                   %cIpacPanel                           
`,'color:#008fff','color:#17d9ff');

import { addAuthenticatedListener, UNAUTHORIZED_REASON_LOGOUT, UNAUTHORIZED_REASON_RUNTIME, addUnauthorizedListener, clearAllStoredData } from './api/core.js';
import { fetchPublicSettings } from './api/settings.js';
import { DEFAULT_UI_REFRESH_INTERVAL_MS, getUrlSearchParam, parseOptionalIntegerInRange, setUrlSearchParams } from './utils/utils.js';

export const state = {
	instances: [],
	historySize: 256,
	currentUser: null,
	isAdmin: false,
	instanceSessionSeq: 0,
	currentInstanceName: null,
	currentInstanceObj: null,
	currentFileRootPath: '',
	webTitle: 'IpacPanel',
	dashboardEnabled: true,
	publicDashboardEnabled: false,
};

export const pageNames = {
	instanceList: 'instance-list',
	dashboard: 'dashboard',
	terminal: 'terminal',
};

const routePages = {
	home: 'home',
	dashboard: 'dashboard',
};

console.log('[模块] UI 加载中...');

export const mainHeader = document.getElementById('mainHeader');
export const mainContainer = document.getElementById('mainContainer');
export const mainModalOverlay = document.getElementById('mainModalOverlay');

let bootErrorRoot = null;

mainHeader.insertAdjacentHTML('beforeend', /*html*/`
	<div class="header-content">
		<div class="brand-nav">
			<span class="logo">IpacPanel</span>
			<nav class="breadcrumb">
				<span class="nav-sep">|</span>
				<span id="navHome" class="nav-item">应用实例</span>
				<span id="navSep" class="nav-sep hidden">|</span>
				<span id="navCurrent" class="nav-item hidden">终端</span>
				<span id="navFileSep" class="nav-sep hidden">|</span>
				<span id="navFiles" class="nav-item hidden">文件</span>
				<span class="nav-sep">|</span>
				<span id="navDashboard" class="nav-item">仪表板</span>
			</nav>
		</div>
	</div>
`);

let instanceListPage = null;
let dashboardPage = null;
let terminalPage = null;
let unauthorizedCleanup = null;
let authenticatedCleanup = null;
let unauthorizedHandling = false;
let unauthorizedLocked = false;
let runtimeEpoch = 0;

const shellDom = {
	header: mainHeader,
	app: mainContainer,
};

const showShell = () => {
	if (shellDom.header) shellDom.header.style.display = '';
	if (shellDom.app) shellDom.app.style.display = '';
};

const hideShell = () => {
	if (shellDom.header) shellDom.header.style.display = 'none';
	if (shellDom.app) shellDom.app.style.display = 'none';
};

const resetRuntimeState = () => {
	state.instances = [];
	state.historySize = 256;
	state.currentUser = null;
	state.isAdmin = false;
	state.instanceSessionSeq += 1;
	state.currentInstanceName = null;
	state.currentInstanceObj = null;
	state.currentFileRootPath = '';
	state.webTitle = 'IpacPanel';
	state.dashboardEnabled = true;
	state.publicDashboardEnabled = false;
};

const normalizeWebTitle = (value) => {
	const title = String(value || '').trim();
	return title || 'IpacPanel';
};

export const applyWebTitle = (value) => {
	const webTitle = normalizeWebTitle(value);
	state.webTitle = webTitle;
	document.title = webTitle;
	const logo = mainHeader.querySelector('.logo');
	if (logo) {
		logo.textContent = webTitle;
	}
	return webTitle;
};

const setDashboardNavigationVisible = (visible) => {
	const navDashboard = document.getElementById('navDashboard');
	if (!navDashboard) {
		return;
	}
	navDashboard.classList.toggle('hidden', !visible);
	const previousSeparator = navDashboard.previousElementSibling;
	if (previousSeparator && previousSeparator.classList.contains('nav-sep')) {
		previousSeparator.classList.toggle('hidden', !visible);
	}
};

const setAuthenticatedNavigationVisible = (visible) => {
	const navHome = document.getElementById('navHome');
	const navSep = document.getElementById('navSep');
	const navCurrent = document.getElementById('navCurrent');
	const navFileSep = document.getElementById('navFileSep');
	const navFiles = document.getElementById('navFiles');
	if (navHome) {
		navHome.classList.toggle('hidden', !visible);
		const previousSeparator = navHome.previousElementSibling;
		if (previousSeparator && previousSeparator.classList.contains('nav-sep')) {
			previousSeparator.classList.toggle('hidden', !visible);
		}
	}
	if (!visible) {
		if (navSep) navSep.classList.add('hidden');
		if (navCurrent) navCurrent.classList.add('hidden');
		if (navFileSep) navFileSep.classList.add('hidden');
		if (navFiles) navFiles.classList.add('hidden');
	}
};

const applyDashboardEnabled = async (enabled, options = {}) => {
	state.dashboardEnabled = !!enabled;
	setDashboardNavigationVisible(state.dashboardEnabled);
	if (state.dashboardEnabled || unauthorizedLocked) {
		return;
	}
	if (options.redirectActivePage === false) {
		return;
	}
	if (!instanceListPage || !dashboardPage || !terminalPage) {
		return;
	}
	if (dashboardPage && typeof dashboardPage.hidePage === 'function') {
		dashboardPage.hidePage();
	}
	const terminalSection = document.getElementById('terminalSection');
	if (terminalSection && terminalSection.classList.contains('active')) {
		return;
	}
	await closeCurrentTerminal();
	setHomeRoute();
	switchPage(pageNames.instanceList);
};

const applyPublicRuntimeSettings = async (data = {}) => {
	applyWebTitle(data.web_title);
	const metrics = data.metrics || {};
	state.publicDashboardEnabled = metrics.public_dashboard === true;
	await applyDashboardEnabled(metrics.enabled !== false, { redirectActivePage: false });
};

const loadPublicRuntimeSettings = async () => {
	const result = await fetchPublicSettings();
	if (!result.ok) {
		throw new Error(result.error || 'Failed to load settings');
	}
	await applyPublicRuntimeSettings(result.data || {});
	return result.data || {};
};

const getRuntimeEpoch = () => runtimeEpoch;

const isRuntimeActive = (epoch) => !unauthorizedLocked && epoch === runtimeEpoch;

const getRoutePageParam = () => String(getUrlSearchParam('p') || '').trim().toLowerCase();

const isPublicDashboardRouteAllowed = () => {
	return getRoutePageParam() === routePages.dashboard && state.dashboardEnabled && state.publicDashboardEnabled;
};

const getRouteInstanceParam = () => String(getUrlSearchParam('i') || '').trim();

const clearRouteParams = (options = {}) => {
	setUrlSearchParams({ p: null, i: null }, options);
};

const setHomeRoute = (options = {}) => {
	setUrlSearchParams({ p: null, i: null }, options);
};

const setDashboardRoute = (options = {}) => {
	setUrlSearchParams({ p: routePages.dashboard, i: null }, options);
};

const setInstanceRoute = (instanceName, options = {}) => {
	const name = String(instanceName || '').trim();
	setUrlSearchParams({ p: null, i: name || null }, options);
};

const scrollTerminalPageToTop = () => {
	const section = document.getElementById('terminalSection');
	if (!section) return;
	section.scrollTop = 0;
	requestAnimationFrame(() => {
		section.scrollTop = 0;
	});
};

const enterUnauthorizedState = async (detail = {}) => {
	if (unauthorizedLocked || unauthorizedHandling) {
		return;
	}
	unauthorizedLocked = true;
	runtimeEpoch += 1;
	unauthorizedHandling = true;
	try {
		if (detail.clearStoredData !== false) {
			clearAllStoredData();
		}
		resetRuntimeState();
		try {
			terminalPage?.closeTerminalPage?.();
		} catch (error) {
			console.error('[UI] 收拢终端页面失败:', error);
		}
		try {
			if (dashboardPage && typeof dashboardPage.hidePage === 'function') {
				dashboardPage.hidePage();
			}
		} catch (error) {
			console.error('[UI] 隐藏仪表板失败:', error);
		}
		try {
			instanceListPage?.hidePage?.();
		} catch (error) {
			console.error('[UI] 隐藏实例列表失败:', error);
		}
		hideShell();
		if (detail.reason === UNAUTHORIZED_REASON_LOGOUT) {
			clearRouteParams({ replace: true });
		}
		if (detail.reload !== false) {
			window.location.reload();
			return;
		}
		const { showAuthPage } = await import('./page/auth.js');
		showAuthPage();
	} finally {
		unauthorizedHandling = false;
	}
};

const ensureBootErrorRoot = () => {
	if (bootErrorRoot) {
		return bootErrorRoot;
	}
	bootErrorRoot = document.createElement('div');
	bootErrorRoot.id = 'bootErrorRoot';
	bootErrorRoot.innerHTML = /*html*/`
		<div class="boot-error-backdrop">
			<div class="boot-error-card-shell">
				<div class="boot-error-card">
					<div class="boot-error-title">APPLICATION START FAILED</div>
					<div class="boot-error-message" id="bootErrorMessage"></div>
					<div class="boot-error-actions">
						<button id="bootErrorRetry" type="button" class="boot-error-btn">RETRY</button>
					</div>
				</div>
			</div>
		</div>
	`;
	document.body.appendChild(bootErrorRoot);
	bootErrorRoot.querySelector('#bootErrorRetry')?.addEventListener('click', () => {
		window.location.reload();
	});
	return bootErrorRoot;
};

const showBootError = (error) => {
	console.error('[UI] 应用启动失败:', error);
	hideShell();
	const root = ensureBootErrorRoot();
	const message = root.querySelector('#bootErrorMessage');
	const text = String(error?.message || 'Failed to initialize the application. Please retry.').trim();
	if (message) {
		message.textContent = text || 'Failed to initialize the application. Please retry.';
	}
	root.classList.add('visible');
};

const hideBootError = () => {
	if (!bootErrorRoot) {
		return;
	}
	bootErrorRoot.classList.remove('visible');
};

const switchPage = (pageName) => {
	if (pageName === pageNames.instanceList) {
		terminalPage.hidePage();
		dashboardPage.hidePage();
		instanceListPage.showPage();
		return;
	}
	if (pageName === pageNames.dashboard) {
		terminalPage.hidePage();
		instanceListPage.hidePage();
		dashboardPage.showPage();
		return;
	}

	instanceListPage.hidePage();
	dashboardPage.hidePage();
	terminalPage.showPage();
};

const canLeaveCurrentContext = async () => {
	if (!terminalPage) {
		return true;
	}
	if (typeof terminalPage.tryLeaveCurrentContext !== 'function') {
		return !terminalPage.hasUnsavedFileEditorChanges();
	}
	return await terminalPage.tryLeaveCurrentContext();
};

const openInstanceTerminal = async (ins, options = {}) => {
	if (unauthorizedLocked) {
		return;
	}
	if (!ins?.name) {
		return;
	}
	if (state.currentInstanceName === ins.name) {
		return;
	}
	if (state.currentInstanceName && !await canLeaveCurrentContext()) {
		const currentName = state.currentInstanceName;
		setInstanceRoute(currentName, { replace: options.replaceRoute === true });
		return;
	}
	const runtimeAtStart = getRuntimeEpoch();
	const sessionId = ++state.instanceSessionSeq;
	terminalPage.prepareOpenTerminalPage?.();
	switchPage(pageNames.terminal);
	scrollTerminalPageToTop();
	terminalPage.openTerminalPage(ins, state.historySize, { sessionId }).catch((error) => {
		if (!isRuntimeActive(runtimeAtStart)) {
			return false;
		}
		console.error('[UI] 打开实例终端失败:', error);
		void leaveTerminalPage();
		return false;
	}).then((ok) => {
		if (ok === false || sessionId !== state.instanceSessionSeq || !isRuntimeActive(runtimeAtStart)) {
			return;
		}
		terminalPage.loadFiles(undefined, { instanceName: ins.name, sessionId });
	});
	setInstanceRoute(ins.name, { replace: options.replaceRoute === true });
};


const closeCurrentTerminal = async () => {
	if (unauthorizedLocked) {
		terminalPage?.closeTerminalPage?.();
		return true;
	}
	if (state.currentInstanceName && !await canLeaveCurrentContext()) {
		setInstanceRoute(state.currentInstanceName);
		return false;
	}
	terminalPage.closeTerminalPage();
	switchPage(pageNames.instanceList);
	return true;
};

const openDashboardPage = async (options = {}) => {
	if (unauthorizedLocked) {
		return;
	}
	if (!state.dashboardEnabled) {
		return;
	}
	if (state.currentInstanceName && !await canLeaveCurrentContext()) {
		setInstanceRoute(state.currentInstanceName, { replace: options.replaceRoute === true });
		return;
	}
	terminalPage.closeTerminalPage();
	setDashboardRoute({ replace: options.replaceRoute === true });
	switchPage(pageNames.dashboard);
};

const leaveTerminalPage = async () => {
	if (unauthorizedLocked) {
		return true;
	}
	const runtimeAtStart = getRuntimeEpoch();
	const closed = await closeCurrentTerminal();
	if (closed === false) {
		return false;
	}
	if (!isRuntimeActive(runtimeAtStart)) {
		return true;
	}
	setHomeRoute();
	await instanceListPage.loadInstances();
	return true;
};

const bindHistoryNavigation = () => {
	window.onpopstate = async () => {
		if (unauthorizedLocked) {
			return;
		}
		const targetPage = getRoutePageParam();
		if (targetPage === routePages.dashboard) {
			await openDashboardPage({ replaceRoute: true });
			return;
		}
		if (targetPage === routePages.home) {
			const closed = await closeCurrentTerminal();
			if (closed !== false) {
				setHomeRoute({ replace: true });
				await instanceListPage.loadInstances();
			}
			return;
		}
		if (targetPage) {
			const closed = await closeCurrentTerminal();
			if (closed !== false) {
				setHomeRoute({ replace: true });
				await instanceListPage.loadInstances();
			}
			return;
		}
		const targetName = getRouteInstanceParam();
		if (targetName) {
			const ins = state.instances.find(instance => instance.name === targetName);
			if (ins) {
				await openInstanceTerminal(ins, { replaceRoute: true });
				return;
			}
			const closed = await closeCurrentTerminal();
			if (closed !== false) {
				setHomeRoute({ replace: true });
				await instanceListPage.loadInstances();
			}
			return;
		}

		const closed = await closeCurrentTerminal();
		if (closed !== false) {
			instanceListPage.loadInstances();
		}
	};
};

const bindMainNavigation = () => {
	const navHome = document.getElementById('navHome');
	const navDashboard = document.getElementById('navDashboard');
	navHome?.addEventListener('click', () => {
		void leaveTerminalPage().then((closed) => {
			if (closed !== false) {
				setHomeRoute();
				switchPage(pageNames.instanceList);
			}
		});
	});
	navDashboard?.addEventListener('click', () => {
		void openDashboardPage();
	});
};

const bindBeforeUnloadProtection = () => {
	window.onbeforeunload = (event) => {
		if (!terminalPage.hasUnsavedFileEditorChanges()) {
			return;
		}

		event.preventDefault();
		return '';
	};
};

const bindRuntimeSettingsApplied = () => {
	window.addEventListener('ipacpanel:settings-applied', (event) => {
		const settings = event.detail?.current || {};
		if (Object.prototype.hasOwnProperty.call(settings, 'webTitle')) {
			applyWebTitle(settings.webTitle);
		}
		if (Number.isFinite(settings.historySize)) {
			state.historySize = settings.historySize;
		}
		if (settings.metrics && Object.prototype.hasOwnProperty.call(settings.metrics, 'enabled')) {
			void applyDashboardEnabled(settings.metrics.enabled);
		}
		if (settings.metrics && Object.prototype.hasOwnProperty.call(settings.metrics, 'publicDashboard')) {
			state.publicDashboardEnabled = settings.metrics.publicDashboard === true;
		}
		terminalPage?.applyRuntimeSettings?.(settings);
	});
};

const bindControlsThrottling = () => {
    document.addEventListener('click', (event) => {
        const btn = event.target.closest('.controls button');
        if (!btn) return;

        const controls = btn.closest('.controls');
        if (!controls || controls.classList.contains('disabled')) return;

        controls.classList.add('disabled');
        setTimeout(() => {
            controls.classList.remove('disabled');
		}, DEFAULT_UI_REFRESH_INTERVAL_MS);
	}, true);
};

const bindUnauthorizedHandling = () => {
	unauthorizedCleanup?.();
	unauthorizedCleanup = addUnauthorizedListener((detail) => {
		void enterUnauthorizedState(detail);
	});
};

const bindAuthenticatedHandling = () => {
	authenticatedCleanup?.();
	authenticatedCleanup = addAuthenticatedListener(() => {
		unauthorizedLocked = false;
		window.location.reload();
	});
};

const bootPublicDashboardPage = async (runtimeAtStart) => {
	state.currentUser = null;
	state.isAdmin = false;
	setAuthenticatedNavigationVisible(false);
	setDashboardNavigationVisible(true);
	const dashboardModule = await import('./page/dashboardPage.js');
	if (runtimeAtStart !== getRuntimeEpoch()) {
		return;
	}
	dashboardPage = dashboardModule.bootDashboardPage();
	bindControlsThrottling();
	setDashboardRoute({ replace: true });
	dashboardPage.showPage();
	showShell();
};

export const main = async () => {
	const runtimeAtStart = getRuntimeEpoch();
	hideBootError();
	bindUnauthorizedHandling();
	bindAuthenticatedHandling();
	await loadPublicRuntimeSettings();
	if (runtimeAtStart !== getRuntimeEpoch()) {
		return;
	}
	const bootAuthState = await import('./api/user.js').then((mod) => mod.resolveBootAuthState());
	if (bootAuthState.status === 'unauthorized') {
		if (isPublicDashboardRouteAllowed()) {
			await bootPublicDashboardPage(runtimeAtStart);
			return;
		}
		await enterUnauthorizedState({
			reason: UNAUTHORIZED_REASON_RUNTIME,
			clearStoredData: false,
			reload: false,
		});
		return;
	}
	if (bootAuthState.status !== 'authenticated') {
		throw bootAuthState.error || new Error('Failed to resolve boot authentication state');
	}
	if (!isRuntimeActive(runtimeAtStart)) {
		return;
	}
	state.currentUser = bootAuthState.user || null;
	state.isAdmin = Number(bootAuthState.user?.perm || 0) === 7;
	setAuthenticatedNavigationVisible(true);
	setDashboardNavigationVisible(state.dashboardEnabled);
	if (!isRuntimeActive(runtimeAtStart)) {
		return;
	}

    const [instanceListModule, terminalModule, dashboardModule] = await Promise.all([
        import('./page/instanceList.js'),
        import('./page/instanceWorkspacePage.js'),
        import('./page/dashboardPage.js'),
    ]);
	if (!isRuntimeActive(runtimeAtStart)) {
		return;
	}

	terminalPage = terminalModule.bootTerminalPage({
		controller: {
			loadInstances: () => instanceListPage.loadInstances(),
			leaveTerminalPage,
			openInstanceTerminal,
	            parseOptionalIntegerInRange,
			showInstanceListPage: () => {
				setHomeRoute();
				switchPage(pageNames.instanceList);
			},
			updateUrl: (instanceName) => {
				if (instanceName) {
					setInstanceRoute(instanceName);
				} else {
					setHomeRoute();
				}
			},
	        },
	    });

	    instanceListPage = instanceListModule.bootInstanceListPage({
		onCreateInstance: (options = {}) => terminalPage.openCreateInstanceModal(options),
		onOpenInstance: (ins) => void openInstanceTerminal(ins),
		onPatchCurrentInstance: (patch) => terminalPage.patchCurrentTerminalInstance?.(patch),
	    });
	dashboardPage = dashboardModule.bootDashboardPage();

	bindHistoryNavigation();
	bindMainNavigation();
	bindBeforeUnloadProtection();
	bindRuntimeSettingsApplied();
	bindControlsThrottling();

	try {
		await instanceListPage.loadInstances();
	} finally {
		if (isRuntimeActive(runtimeAtStart)) {
			showShell();
		}
	}
	if (!isRuntimeActive(runtimeAtStart)) {
		return;
	}

	const targetPage = getRoutePageParam();
	if (targetPage === routePages.dashboard) {
		if (state.dashboardEnabled) {
			setDashboardRoute({ replace: true });
			await openDashboardPage({ replaceRoute: true });
			return;
		}
		setHomeRoute({ replace: true });
	}
	if (targetPage === routePages.home) {
		setHomeRoute({ replace: true });
		await closeCurrentTerminal();
		if (isRuntimeActive(runtimeAtStart)) {
			switchPage(pageNames.instanceList);
		}
		return;
	}
	if (targetPage) {
		setHomeRoute({ replace: true });
		await closeCurrentTerminal();
		if (isRuntimeActive(runtimeAtStart)) {
			switchPage(pageNames.instanceList);
		}
		return;
	}

	const targetName = getRouteInstanceParam();
	if (targetName) {
		const ins = state.instances.find(instance => instance.name === targetName);
		if (ins) {
			setInstanceRoute(ins.name, { replace: true });
			await openInstanceTerminal(ins, { replaceRoute: true });
			return;
		}
		setHomeRoute({ replace: true });
	}

	await closeCurrentTerminal();
	if (isRuntimeActive(runtimeAtStart)) {
		setHomeRoute({ replace: true });
		switchPage(pageNames.instanceList);
	}
};

main().catch((error) => {
	showBootError(error);
});
