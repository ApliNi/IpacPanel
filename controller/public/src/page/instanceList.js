import { mainContainer, state } from "../ui.js";
import { bootUserManageModal } from '../module/userManageModal.js';
import { cleanupFileLastDirs } from '../module/fileList.js';
import { bootInstanceGroupModal } from '../module/instanceGroupModal.js';
import { copyTextToClipboard } from '../module/terminal.js';
import { bootPanelSettingsModal } from '../module/panelSettingsModal.js';
import { instanceStatusStore } from '../store/instanceStatusStore.js';
import { InputValidation } from '../utils/inputValidation.js';

// 筛选按钮 localStorage 持久化
const FILTER_STORAGE_KEY = 'ipac_instance_list_filter';
const VALID_FILTERS = Object.freeze(['all', 'group', 'running', 'stopped']);
const DEFAULT_FILTER = 'group';

const loadSavedFilter = () => {
	try {
		const stored = localStorage.getItem(FILTER_STORAGE_KEY);
		if (stored !== null) {
			if (VALID_FILTERS.includes(stored)) {
				return stored;
			}
			console.warn(`[服务列表页] localStorage 中存储的筛选值 "${stored}" 无效, 回退至默认值`);
		}
	} catch (err) {
		console.warn('[服务列表页] 读取 localStorage 失败:', err);
	}
	return DEFAULT_FILTER;
};

const saveFilter = (value) => {
	if (!VALID_FILTERS.includes(value)) {
		console.warn(`[服务列表页] 尝试保存无效的筛选值 "${value}", 已拒绝写入`);
		return;
	}
	try {
		localStorage.setItem(FILTER_STORAGE_KEY, value);
	} catch (err) {
		console.error('[服务列表页] 写入 localStorage 失败:', err);
	}
};

console.log('[页面] 服务列表页加载中...');

mainContainer.insertAdjacentHTML('beforeend', /*html*/`
	<section id="instanceListSection" class="section">
		<div class="section-header">
			<div class="search-bar-container">
				<div class="filter-group">
					<button class="filter-btn" data-filter="all">ALL</button>
					<button class="filter-btn" data-filter="group">GROUP</button>
					<button class="filter-btn" data-filter="running">RUN</button>
					<button class="filter-btn" data-filter="stopped">STOP</button>
				</div>
				<input type="text" id="instanceSearch" placeholder=" SEARCH INSTANCE" autocomplete="off" maxlength="${InputValidation.limits.fileSearch}">
				<div class="instance-actions">
					<button id="createInstanceBtn" class="action-btn">NEW</button>
					<button id="userManageBtn" class="action-btn" type="button">USER</button>
					<button id="panelSettingsBtn" class="action-btn" type="button">SETTINGS</button>
				</div>
			</div>
		</div>
		<div id="listContainer"></div>
	</section>
`);

const dom = {
	section: document.getElementById('instanceListSection'),
	listContainer: document.getElementById('listContainer'),
	instanceSearch: document.getElementById('instanceSearch'),
	filterBtns: document.querySelectorAll('#instanceListSection .filter-btn'),
	createInstanceBtn: document.getElementById('createInstanceBtn'),
	panelSettingsBtn: document.getElementById('panelSettingsBtn'),
	userManageBtn: document.getElementById('userManageBtn'),
};

const pageState = {
    currentFilter: loadSavedFilter(),
	initialLoadDone: false,
	unsubscribeStatusStore: null,
};

let userManageModal = null;
let instanceGroupModal = null;
let panelSettingsModal = null;
let onPatchCurrentInstance = null;

// Cache instance lookups for click handler: avoids O(n) find() per click.
const instanceIndex = {
	byName: new Map(),
	ref: null,
};

const copiedAccessLinks = new Map();

const ACCESS_LINK_SUCCESS_MS = 3000;

const getAccessLinkStateKey = (instanceName, linkName) => `${instanceName}::${linkName}`;

const getDisplayAccessLinkName = (name) => Array.from(String(name || '')).slice(0, 7).join('');

const isBrowserOpenableAccessLink = (value) => /^https?:\/\//i.test(String(value || '').trim());

const parseProtocolAccessLinkLine = (line) => {
	const matched = String(line || '').trim().match(/^([a-z][a-z0-9+.-]*):\/\/.+$/i);
	if (!matched) {
		return null;
	}
	return [matched[1].toLowerCase(), matched[0]];
};

const parseAccessLinksEntries = (accessLinks) => {
	const lines = String(accessLinks || '').split(/\r?\n/);
	const entries = [];
	const seen = new Set();
	for (let index = 0; index < lines.length; index += 1) {
		const line = String(lines[index] || '').trim();
		if (!line || /^#/.test(line)) {
			continue;
		}
		const protocolEntry = parseProtocolAccessLinkLine(line);
		if (protocolEntry) {
			const dedupeKey = protocolEntry[0].toLowerCase();
			if (seen.has(dedupeKey)) {
				continue;
			}
			seen.add(dedupeKey);
			entries.push(protocolEntry);
			continue;
		}
		const colonIndex = line.indexOf(':');
		if (colonIndex <= 0) {
			continue;
		}
		const name = line.slice(0, colonIndex).trim();
		const value = line.slice(colonIndex + 1).trim();
		if (!name || !value) {
			continue;
		}
		const dedupeKey = name.toLowerCase();
		if (seen.has(dedupeKey)) {
			continue;
		}
		seen.add(dedupeKey);
		entries.push([name, value]);
	}
	return entries;
};

const escapeAttrSelectorValue = (value) => {
	const text = String(value || '');
	if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') {
		return CSS.escape(text);
	}
	return text.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
};

export const renderInstanceList = (instances, onSelect) => {
	const baseInstances = getBaseInstancesForCounts();
	if (pageState.currentFilter === 'all') {
		setGroupViewMode('all');
		renderGroupDetailsList([
			{
				key: ALL_GROUP_KEY,
				label: ALL_GROUP_LABEL,
				className: 'instance-group-all',
				instances,
				visibleCount: instances.length,
				totalCount: baseInstances.length,
			},
		], onSelect);
		return;
	}

	setGroupViewMode('groups');
	const groupEntries = getGroupBuckets(instances);
	const totalBuckets = new Map();
	baseInstances.forEach(svc => {
		const g = normalizeGroupName(svc.group);
		totalBuckets.set(g, (totalBuckets.get(g) || 0) + 1);
	});

	renderGroupDetailsList(groupEntries.map(([groupName, groupInstances]) => {
		const totalCount = totalBuckets.get(groupName) || groupInstances.length;
		return {
			key: groupName,
			label: groupName,
			className: '',
			instances: groupInstances,
			visibleCount: groupInstances.length,
			totalCount,
		};
	}), onSelect);
};

const UNGROUPED_LABEL = 'UNGROUPED';
const ALL_GROUP_KEY = '__all__';
const ALL_GROUP_LABEL = 'ALL';

const normalizeGroupName = (value) => {
	const v = String(value || '').trim();
	return v ? v : UNGROUPED_LABEL;
};

const groupSortKey = (groupName) => {
	if (groupName === UNGROUPED_LABEL) {
		return '';
	}
	return groupName;
};

const setGroupViewMode = (mode) => {
	if (!dom.listContainer) {
		return;
	}
	const current = dom.listContainer.dataset.groupView || '';
	if (current !== mode) {
		dom.listContainer.innerHTML = '';
	}
	dom.listContainer.dataset.groupView = mode;
};

const getBaseInstancesForCounts = () => {
	const list = state.instances.filter(svc => {
		if (pageState.currentFilter === 'running') return svc.running;
		if (pageState.currentFilter === 'stopped') return !svc.running;
		return true;
	});
	list.sort((a, b) => a.name.localeCompare(b.name));
	return list;
};

const formatCountText = (visibleCount, totalCount) => {
	if (!dom.instanceSearch) {
		return String(totalCount);
	}
	const q = (dom.instanceSearch.value || '').trim();
	if (!q) {
		return String(totalCount);
	}
	return `${visibleCount} / ${totalCount}`;
};

const setItemHTMLAndSig = (item, svc) => {
	if (!item || !svc) {
		return;
	}

	const accessLinkSig = String(svc.access_links || '');
	const currentSig = `${svc.running}-${svc.updating}-${svc.restarting}-${svc.start_time}-${svc.restart_count}-${accessLinkSig}`;
	if (item.getAttribute('data-sig') === currentSig) {
		return;
	}

	// Avoid innerHTML to prevent XSS and DOM breakage when instance fields
	// contain special characters.
	let statusText = svc.running ? 'RUN' : 'STOP';
	let statusClass = svc.running ? 'status-online' : 'status-offline';
	if (svc.restarting) {
		statusText = 'RESTART';
		statusClass = 'status-restart';
	} else if (svc.updating) {
		statusText = 'UPDATE';
		statusClass = 'status-restart';
	}

	let statusEl = item.querySelector('.item-status');
	let titleEl = item.querySelector('h3');
	let metaEl = item.querySelector('.instance-meta-row');
	let metaTextEl = item.querySelector('.instance-meta-text');
	let timeEl = item.querySelector('.instance-meta-time');
	let restartEl = item.querySelector('.instance-meta-restart');
	let accessLinksEl = item.querySelector('.instance-access-links');

	if (!statusEl || !titleEl || !metaEl || !metaTextEl || !timeEl || !restartEl || !accessLinksEl) {
		item.textContent = '';
		statusEl = document.createElement('span');
		statusEl.className = 'item-status';
		titleEl = document.createElement('h3');
		metaEl = document.createElement('div');
		metaEl.className = 'instance-meta-row';
		metaTextEl = document.createElement('div');
		metaTextEl.className = 'instance-meta-text';
		timeEl = document.createElement('p');
		timeEl.className = 'instance-meta-time';
		restartEl = document.createElement('p');
		restartEl.className = 'instance-meta-restart';
		accessLinksEl = document.createElement('div');
		accessLinksEl.className = 'instance-access-links';
		metaTextEl.appendChild(timeEl);
		metaTextEl.appendChild(restartEl);
		metaEl.appendChild(metaTextEl);
		metaEl.appendChild(accessLinksEl);
		item.appendChild(statusEl);
		item.appendChild(titleEl);
		item.appendChild(metaEl);
	}

	statusEl.textContent = `[${statusText}]`;
	statusEl.classList.remove('status-online', 'status-offline', 'status-restart');
	statusEl.classList.add(statusClass);

	titleEl.textContent = String(svc.name || '');
	titleEl.title = String(svc.name || '');
	timeEl.textContent = `T: ${svc.start_time || '#'}`;
	restartEl.textContent = `R: ${svc.restart_count || '#'}`;

	accessLinksEl.replaceChildren();
	const accessLinks = parseAccessLinksEntries(svc.access_links);
	if (accessLinks.length > 0) {
		const frag = document.createDocumentFragment();
		accessLinks.forEach(([name, url]) => {
			const shouldOpen = isBrowserOpenableAccessLink(url);
			const a = document.createElement('a');
			a.className = 'instance-access-link';
			a.dataset.linkName = name;
			a.dataset.linkUrl = url;
			a.dataset.linkAction = shouldOpen ? 'open' : 'copy';
			a.title = url;
			a.setAttribute('aria-label', `${shouldOpen ? 'Open' : 'Copy'} access link: ${name}`);
			a.textContent = `[${getDisplayAccessLinkName(name)}]`;
			if (shouldOpen) {
				a.href = url;
				a.target = '_blank';
				a.rel = 'noopener noreferrer';
			} else {
				a.href = '#';
			}
			if (copiedAccessLinks.has(getAccessLinkStateKey(svc.name, name))) {
				a.classList.add('is-copied');
			}
			frag.appendChild(a);
		});
		accessLinksEl.appendChild(frag);
	}

	item.setAttribute('data-sig', currentSig);
};

const markAccessLinkCopied = (instanceName, linkName) => {
	const key = getAccessLinkStateKey(instanceName, linkName);
	const currentTimer = copiedAccessLinks.get(key);
	if (currentTimer) {
		clearTimeout(currentTimer);
	}
	const timer = setTimeout(() => {
		copiedAccessLinks.delete(key);
		const card = findInstanceCard(instanceName);
		if (!card) return;
		const link = card.querySelector(`.instance-access-link[data-link-name="${escapeAttrSelectorValue(linkName)}"]`);
		if (!link) return;
		link.classList.remove('is-copied');
	}, ACCESS_LINK_SUCCESS_MS);
	copiedAccessLinks.set(key, timer);
	const card = findInstanceCard(instanceName);
	if (!card) return;
	const link = card.querySelector(`.instance-access-link[data-link-name="${escapeAttrSelectorValue(linkName)}"]`);
	if (!link) return;
	link.classList.add('is-copied');
};

const handleInstanceAccessLinkClick = async (link) => {
	if (!link) return;
	if (link.dataset.linkAction === 'open') {
		const card = link.closest('.instance-item');
		if (!card) return;
		markAccessLinkCopied(
			String(card.dataset.name || '').trim(),
			String(link.dataset.linkName || '').trim(),
		);
		return;
	}
	const card = link.closest('.instance-item');
	if (!card) return;
	const instanceName = String(card.dataset.name || '').trim();
	const linkName = String(link.dataset.linkName || '').trim();
	const linkURL = String(link.dataset.linkUrl || '').trim();
	if (!instanceName || !linkName || !linkURL) {
		return;
	}
	await copyTextToClipboard(linkURL);
	markAccessLinkCopied(instanceName, linkName);
};

const renderFlatList = (instances, onSelect) => {
	const grid = dom.listContainer;
	syncInstanceCards(grid, instances, onSelect);
};

const getGroupBuckets = (instances) => {
	const buckets = new Map();
	instances.forEach(svc => {
		const g = normalizeGroupName(svc.group);
		if (!buckets.has(g)) {
			buckets.set(g, []);
		}
		buckets.get(g).push(svc);
	});

	const entries = Array.from(buckets.entries());
	entries.sort((a, b) => groupSortKey(a[0]).localeCompare(groupSortKey(b[0])));
	entries.forEach(entry => {
		entry[1].sort((a, b) => a.name.localeCompare(b.name));
	});
	return entries;
};

const ensureGroupNode = (groupName, className = '') => {
	let details = null;
	const nodes = dom.listContainer.querySelectorAll('details.instance-group');
	for (const node of nodes) {
		if (node.dataset.group === groupName) {
			details = node;
			break;
		}
	}
	if (!details) {
		details = document.createElement('details');
		details.className = className ? `instance-group ${className}` : 'instance-group';
		details.dataset.group = groupName;
		details.open = true;
		const summary = document.createElement('summary');
		summary.className = 'instance-group-summary';
		const content = document.createElement('div');
		content.className = 'instance-group-grid';
		details.appendChild(summary);
		details.appendChild(content);
	} else if (className) {
		details.classList.add(className);
	}
	return details;
};

const ensureInstanceIndex = () => {
	// Index is rebuilt only when instances array identity changes.
	const instances = state.instances || [];
	if (instanceIndex.ref === instances) {
		return;
	}
	instanceIndex.byName.clear();
	for (let i = 0; i < instances.length; i += 1) {
		const svc = instances[i];
		const name = String(svc?.name || '');
		if (name) {
			instanceIndex.byName.set(name, svc);
		}
	}
	instanceIndex.ref = instances;
};

const findInstanceCard = (name) => {
	if (!dom.listContainer || !name) {
		return null;
	}
	return dom.listContainer.querySelector(`.instance-item[data-instance-key="${escapeAttrSelectorValue(name)}"]`);
};

const applyInstanceCardEnterMotion = (item, index) => {
	if (!item) {
		return;
	}
	item.style.setProperty('--instance-enter-delay', `${Math.min(index, 8) * 45}ms`);
	item.classList.remove('instance-item-enter');
	void item.offsetWidth;
	item.classList.add('instance-item-enter');
	item.addEventListener('animationend', () => {
		item.classList.remove('instance-item-enter');
		item.style.removeProperty('--instance-enter-delay');
	}, { once: true });
};

const syncInstanceCards = (grid, instances, onSelect) => {
	if (!grid) {
		return;
	}

	const currentItems = Array.from(grid.querySelectorAll('.instance-item'));
	const wanted = new Set((instances || []).map((s) => String(s?.name || '')));
	const byName = new Map();
	const previousNames = new Set();
	for (const item of currentItems) {
		const n = String(item?.dataset?.name || '');
		if (n) {
			previousNames.add(n);
			byName.set(n, item);
		}
		if (!wanted.has(n)) {
			item.remove();
		}
	}
	const removedNames = Array.from(previousNames).filter((name) => !wanted.has(name));
	const addedNames = Array.from(wanted).filter((name) => !previousNames.has(name));
	const shouldAnimateWholeGroup = removedNames.length > 0;
	const addedNameSet = new Set(addedNames);
	const enterItems = [];

	const frag = document.createDocumentFragment();
	(instances || []).forEach((svc, index) => {
		const name = String(svc?.name || '');
		if (!name) {
			return;
		}
		let item = byName.get(name) || null;
		const isNew = !item;
		if (isNew) {
			item = document.createElement('div');
			item.className = 'instance-item';
		}
		item.dataset.name = name;
		item.dataset.instanceKey = name;

		setItemHTMLAndSig(item, svc);
		if (shouldAnimateWholeGroup || (isNew && addedNameSet.has(name))) {
			enterItems.push([item, index]);
		}
		frag.appendChild(item);
	});
	grid.appendChild(frag);
	enterItems.forEach(([item, index]) => applyInstanceCardEnterMotion(item, index));
};

const renderGroupDetailsList = (groups, onSelect) => {
	const existingGroups = Array.from(dom.listContainer.querySelectorAll('details.instance-group'));
	const wantedGroups = new Set(groups.map(g => g.key));

	existingGroups.forEach(g => {
		if (!wantedGroups.has(g.dataset.group)) {
			g.remove();
		}
	});

	// Build a map to avoid scanning all <details> for each group.
	const existingByGroup = new Map();
	for (let i = 0; i < existingGroups.length; i += 1) {
		const node = existingGroups[i];
		const key = String(node?.dataset?.group || '');
		if (key) {
			existingByGroup.set(key, node);
		}
	}

	const frag = document.createDocumentFragment();
	groups.forEach((group) => {
		let details = existingByGroup.get(group.key) || null;
		if (!details) {
			details = ensureGroupNode(group.key, group.className || '');
		} else if (group.className) {
			details.classList.add(group.className);
		}
		const summary = details.querySelector('summary');
		const grid = details.querySelector('.instance-group-grid');
		if (summary) {
			const labelText = `${group.label} [${formatCountText(group.visibleCount, group.totalCount)}]`;
			let label = summary.querySelector('.instance-group-label');
			let actions = summary.querySelector('.instance-group-actions');
			let btn = summary.querySelector('.instance-group-btn');
			if (!label) {
				label = document.createElement('span');
				label.className = 'instance-group-label';
				summary.textContent = '';
				summary.appendChild(label);
			}
			label.textContent = labelText;
			if (!actions) {
				actions = document.createElement('span');
				actions.className = 'instance-group-actions';
				summary.appendChild(actions);
			}
			if (!btn) {
				btn = document.createElement('button');
				btn.type = 'button';
				btn.className = 'instance-group-btn';
				btn.textContent = '#';
				actions.appendChild(btn);
			}
			btn.dataset.group = group.key;
		}
		syncInstanceCards(grid, group.instances || [], onSelect);
		frag.appendChild(details);
	});
	dom.listContainer.appendChild(frag);
};

export const getFilteredInstances = () => {
    if (!dom.instanceSearch) {
        console.error('[服务列表页] DOM 中未找到 instanceSearch 元素');
        return [];
    }

	const normalizedSearch = InputValidation.truncateText(dom.instanceSearch.value || '', InputValidation.limits.fileSearch);
	if (dom.instanceSearch.value !== normalizedSearch) dom.instanceSearch.value = normalizedSearch;
	const searchText = normalizedSearch.toLowerCase();
	const hasSearch = !!searchText;
	const isAllMode = pageState.currentFilter === 'all';
	const isGroupMode = pageState.currentFilter === 'group' || pageState.currentFilter === 'running' || pageState.currentFilter === 'stopped';

	const filteredAll = state.instances.filter(svc => {
		if (pageState.currentFilter === 'running' && !svc.running) return false;
		if (pageState.currentFilter === 'stopped' && svc.running) return false;

		if (!hasSearch) {
			return true;
		}

		const nameMatch = svc.name.toLowerCase().includes(searchText);
		if (isAllMode) {
			return nameMatch;
		}
		if (!isGroupMode) {
			return nameMatch;
		}

		const groupName = normalizeGroupName(svc.group);
		const groupMatch = groupName.toLowerCase().includes(searchText);
		return nameMatch || groupMatch;
	});

	filteredAll.sort((a, b) => a.name.localeCompare(b.name));

	if (!hasSearch || isAllMode) {
		return filteredAll;
	}
	if (!isGroupMode) {
		return filteredAll;
	}

	const matchedGroups = new Set();
	filteredAll.forEach(svc => {
		const groupName = normalizeGroupName(svc.group);
		if (groupName.toLowerCase().includes(searchText)) {
			matchedGroups.add(groupName);
		}
	});
	if (matchedGroups.size === 0) {
		return filteredAll;
	}
	return filteredAll.filter(svc => matchedGroups.has(normalizeGroupName(svc.group)) || svc.name.toLowerCase().includes(searchText));
};

export const bindInstanceListEvents = ({ onCreateInstance, onOpenInstance, onApplyFilters }) => {
    if (dom.instanceSearch) {
        dom.instanceSearch.oninput = () => {
            onApplyFilters();
        };
    } else {
        console.error('[服务列表页] 绑定 oninput 失败: instanceSearch 元素缺失');
    }

	if (dom.filterBtns && dom.filterBtns.length > 0) {
		dom.filterBtns.forEach(btn => {
			btn.onclick = () => {
				dom.filterBtns.forEach(b => b.classList.remove('active'));
				btn.classList.add('active');
				const filterValue = btn.dataset.filter;
				pageState.currentFilter = filterValue;
				saveFilter(filterValue);
				onApplyFilters();
			};
		});
    } else {
        console.warn('[服务列表页] 未找到筛选按钮');
    }

	dom.createInstanceBtn.onclick = () => {
		onCreateInstance();
	};
	if (dom.userManageBtn) {
		dom.userManageBtn.onclick = () => {
			userManageModal?.open?.();
		};
	}
	if (dom.panelSettingsBtn) {
		dom.panelSettingsBtn.classList.toggle('hidden', !state.isAdmin);
		dom.panelSettingsBtn.onclick = () => {
			if (!state.isAdmin) return;
			panelSettingsModal?.open?.();
		};
	}

	dom.listContainer.onclick = (event) => {
		const groupBtn = event.target.closest('.instance-group-btn');
		if (groupBtn) {
			event.preventDefault();
			event.stopPropagation();
			const groupName = String(groupBtn.dataset.group || '').trim();
			const instances = Array.isArray(state.instances) ? state.instances : [];
			const groupInstances = instances.filter((svc) => normalizeGroupName(svc.group) === groupName);
			const groupSet = new Set();
			instances.forEach((svc) => {
				const g = normalizeGroupName(svc?.group);
				if (g) groupSet.add(g);
			});
			const existingGroups = Array.from(groupSet);
			instanceGroupModal?.open?.({
				groupName,
				createGroupName: groupName === UNGROUPED_LABEL ? '' : groupName,
				instances: groupInstances,
				existingGroups,
			});
			return;
		}
		const accessLink = event.target.closest('.instance-access-link');
		if (accessLink) {
			event.stopPropagation();
			if (accessLink.dataset.linkAction !== 'open') {
				event.preventDefault();
			}
			void handleInstanceAccessLinkClick(accessLink);
			return;
		}
		const item = event.target.closest('.instance-item');
		if (!item) {
			return;
		}
		ensureInstanceIndex();
		const svc = instanceIndex.byName.get(String(item.dataset.name || '')) || null;
		if (svc) {
			onOpenInstance(svc);
		}
	};
};

const applyFiltersAndRender = (onOpenInstance) => {
    renderInstanceList(getFilteredInstances(), onOpenInstance);
};

const applyInstanceData = (data, onOpenInstance) => {
	if (!Array.isArray(data)) {
		return false;
	}
	state.instances = data;
	resolveInitialLoad(state.instances);
	cleanupFileLastDirs(state.instances.map((svc) => svc?.name));
	applyFiltersAndRender(onOpenInstance);
	return true;
};

const applyInstancePatch = (changedNames, allInstances) => {
	const snapshotMap = new Map();
	for (const svc of allInstances) {
		const name = String(svc?.name || '');
		if (name) snapshotMap.set(name, svc);
	}

	resolveInitialLoad(state.instances);

	// 原地合并变更数据 (保留数组引用, instanceIndex 缓存有效)
	for (let i = 0; i < state.instances.length; i++) {
		const svc = state.instances[i];
		const name = String(svc?.name || '');
		if (changedNames.has(name)) {
			const updated = snapshotMap.get(name);
			if (updated) Object.assign(svc, updated);
		}
	}

	// 只处理变更的卡片, 不触及其他任何 DOM 元素
	for (const name of changedNames) {
		const svc = snapshotMap.get(name);
		if (!svc) continue;

		const card = findInstanceCard(name);
		if (!card) continue; // 不在 DOM 中 (筛选/搜索已隐藏), 数据已更新无需处理

		// 在 RUN/STOP 筛选模式下, 若运行状态切换导致不再匹配, 仅移除该卡片
		if (pageState.currentFilter === 'running' && !svc.running) {
			card.remove();
			continue;
		}
		if (pageState.currentFilter === 'stopped' && svc.running) {
			card.remove();
			continue;
		}

		// 卡片应保留, 只更新其内容
		setItemHTMLAndSig(card, svc);
	}
};

const resolveInitialLoad = (instances) => {
	if (pageState.initialLoadDone) {
		return;
	}
	pageState.initialLoadDone = true;
	return instances;
};

const ensureInstanceStatusSubscription = (onOpenInstance) => {
	if (pageState.unsubscribeStatusStore) return;
	pageState.unsubscribeStatusStore = instanceStatusStore.subscribe((snapshot, changedNames) => {
		if (!snapshot.ready) return;
		if (changedNames) {
			applyInstancePatch(changedNames, snapshot.instances);
		} else {
			applyInstanceData(snapshot.instances, onOpenInstance);
		}
		if (state.currentInstanceName) {
			const current = snapshot.instances.find((svc) => String(svc?.name || '') === String(state.currentInstanceName));
			if (current) onPatchCurrentInstance?.(current);
		}
	});
};

const loadInstances = async (onOpenInstance) => {
	ensureInstanceStatusSubscription(onOpenInstance);
	instanceStatusStore.start();
	if (!pageState.initialLoadDone) {
		const snapshot = await instanceStatusStore.waitForReady();
		applyInstanceData(snapshot.instances, onOpenInstance);
	}
	return state.instances;
};

const showPage = (onOpenInstance) => {
    dom.section.classList.add('active');
	ensureInstanceStatusSubscription(onOpenInstance);
	instanceStatusStore.start();
};

const hidePage = () => {
    dom.section.classList.remove('active');
};

export const bootInstanceListPage = (options = {}) => {
    const { onCreateInstance, onOpenInstance } = options;
	onPatchCurrentInstance = typeof options.onPatchCurrentInstance === 'function' ? options.onPatchCurrentInstance : null;
	userManageModal = bootUserManageModal();
	panelSettingsModal = bootPanelSettingsModal();
	instanceGroupModal = bootInstanceGroupModal({
		onReload: () => loadInstances(onOpenInstance),
		onCreateInstance: (options = {}) => onCreateInstance?.(options),
	});
    bindInstanceListEvents({
        onCreateInstance,
        onOpenInstance,
        onApplyFilters: () => applyFiltersAndRender(onOpenInstance),
    });

	// 同步筛选按钮 active 状态与 pageState.currentFilter 一致
	dom.filterBtns.forEach(btn => {
		btn.classList.toggle('active', btn.dataset.filter === pageState.currentFilter);
	});

	return {
		loadInstances: () => loadInstances(onOpenInstance),
		showPage: () => showPage(onOpenInstance),
		hidePage,
	};
};
