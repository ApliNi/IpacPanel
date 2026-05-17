import { mainModalOverlay } from "../ui.js";
import { clearTimer, withActionsDisabled } from '../utils/utils.js';
import { formatUserPerm, userPermOptions } from '../utils/enum.js';
import { logout, resetToken } from '../api/auth.js';
import { clearAllStoredData, clearAllStoredDataAndEnterUnauthorizedState, dispatchUnauthorized } from '../api/core.js';
import { instanceStatusStore } from '../store/instanceStatusStore.js';
import { createAdminUser, deleteAdminUser, fetchAdminUser, fetchMe, fetchUsers, updateAdminUser, updateMe } from '../api/user.js';
import { showAlert, showConfirm } from './dialog.js';

console.log('[模块] UserManageModal 加载中...');

const USER_NAME_MAX_LENGTH = 32;
const USER_PASSWORD_MAX_LENGTH = 4096;
const USER_SCOPE_MAX_ITEMS = 4096;
const USER_SCOPE_TEXT_MAX_LENGTH = USER_SCOPE_MAX_ITEMS * (USER_NAME_MAX_LENGTH + 1);
const truncateText = (value, maxLength) => Array.from(String(value || '')).slice(0, maxLength).join('');

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="userManageModal" class="modal-overlay">
		<div class="modal-card user-manage-modal-card">
			<div class="modal-header">
				<span class="modal-title">USER</span>
				<button id="userManageClose" class="modal-close" type="button">×</button>
			</div>
			<div class="modal-form user-manage-form">
				<div class="filter-group user-manage-tabs">
					<button id="userManageTabMe" class="filter-btn active" type="button" data-page="me">ME</button>
					<button id="userManageTabUsers" class="filter-btn" type="button" data-page="users">USERS</button>
					<button id="userManageTabEdit" class="filter-btn" type="button" data-page="edit">EDIT</button>
				</div>
				<div class="user-manage-body">
					<form id="userManagePageMe" class="user-manage-page user-manage-page-me active">
						<div class="field-group">
							<span>NAME</span>
							<input id="userManageMeName" type="text" maxlength="32" autocomplete="off">
						</div>
						<div class="user-manage-pass-row">
							<div class="field-group user-manage-pass-field">
								<span>PASSWORD</span>
								<input id="userManageMePass" type="password" maxlength="4096" autocomplete="new-password" placeholder=" Empty to keep">
							</div>
							<div class="field-group user-manage-pass-confirm">
								<span>CONFIRM PASSWORD</span>
								<input id="userManageMePass2" type="password" maxlength="4096" autocomplete="new-password" placeholder=" Repeat password">
							</div>
						</div>
						<div id="userManageMeActions" class="modal-actions">
							<span id="userManageMeStatus" aria-live="polite"></span>
							<button class="btn" type="button" id="userManageResetToken">RESET TOKEN</button>
							<button class="btn" type="button" id="userManageLogout">LOGOUT</button>
							<span class="controls-divider" aria-hidden="true">|</span>
							<button class="btn" type="button" id="userManageCancel">CLOSE</button>
							<button class="btn btn-start" type="submit" id="userManageMeSave">SAVE</button>
						</div>
					</form>
					<div id="userManagePageUsers" class="user-manage-page">
						<div class="field-group">
							<div id="userManageList" class="user-manage-list"></div>
						</div>
						<div id="userManageActions" class="modal-actions">
							<button class="btn" type="button" id="userManageCancel2">CLOSE</button>
						</div>
					</div>
					<form id="userManagePageEdit" class="user-manage-page user-manage-page-edit" novalidate>
						<div class="field-group">
							<span>USER</span>
							<div class="select-wrapper"><select id="userManageEditSelect" autocomplete="off"></select></div>
						</div>
						<div class="user-manage-user-pass-row">
							<div class="field-group">
								<span>NAME</span>
								<input id="userManageEditName" type="text" maxlength="32" autocomplete="off">
							</div>
							<div class="field-group">
								<span>PASSWORD</span>
								<input id="userManageEditPass" type="password" maxlength="4096" autocomplete="new-password" placeholder=" Empty to keep">
							</div>
						</div>
						<div class="field-group">
							<span>PERM</span>
							<div class="select-wrapper"><select id="userManageEditPerm" autocomplete="off"></select></div>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span>ALLOW GROUPS</span>
							<div class="user-manage-scope-row">
								<div class="select-wrapper">
									<select id="userManageEditGroupAdd" autocomplete="off"></select>
								</div>
								<button id="userManageEditGroupAddBtn" class="btn" type="button">ADD</button>
							</div>
							<textarea id="userManageEditGroups" rows="3" maxlength="${USER_SCOPE_TEXT_MAX_LENGTH}" placeholder=" one per line"></textarea>
						</div>
						<div class="field-group field-group-dynamic-label">
							<span>ALLOW INSTANCES</span>
							<div class="user-manage-scope-row">
								<div class="select-wrapper">
									<select id="userManageEditInstanceAdd" autocomplete="off"></select>
								</div>
								<button id="userManageEditInstanceAddBtn" class="btn" type="button">ADD</button>
							</div>
							<textarea id="userManageEditInstances" rows="4" maxlength="${USER_SCOPE_TEXT_MAX_LENGTH}" placeholder=" one per line"></textarea>
						</div>
						<div id="userManageEditActions" class="modal-actions">
							<span id="userManageEditStatus" aria-live="polite"></span>
							<button class="btn" type="button" id="userManageEditDelete">DELETE</button>
							<span class="controls-divider" aria-hidden="true">|</span>
							<button class="btn" type="button" id="userManageEditClose">CLOSE</button>
							<button class="btn btn-start" type="submit" id="userManageEditSave">SAVE</button>
						</div>
					</form>
				</div>
			</div>
		</div>
	</div>
`);

const dom = {
	modal: document.getElementById('userManageModal'),
	close: document.getElementById('userManageClose'),
	cancel: document.getElementById('userManageCancel'),
	cancel2: document.getElementById('userManageCancel2'),
	actions: document.getElementById('userManageActions'),
	list: document.getElementById('userManageList'),
	tabMe: document.getElementById('userManageTabMe'),
	tabUsers: document.getElementById('userManageTabUsers'),
	tabEdit: document.getElementById('userManageTabEdit'),
	pageMe: document.getElementById('userManagePageMe'),
	pageUsers: document.getElementById('userManagePageUsers'),
	pageEdit: document.getElementById('userManagePageEdit'),
	meName: document.getElementById('userManageMeName'),
	mePass: document.getElementById('userManageMePass'),
	mePass2: document.getElementById('userManageMePass2'),
	meActions: document.getElementById('userManageMeActions'),
	meStatus: document.getElementById('userManageMeStatus'),
	meSave: document.getElementById('userManageMeSave'),
	logout: document.getElementById('userManageLogout'),
	resetToken: document.getElementById('userManageResetToken'),
	editActions: document.getElementById('userManageEditActions'),
	editStatus: document.getElementById('userManageEditStatus'),
	editSave: document.getElementById('userManageEditSave'),
	editClose: document.getElementById('userManageEditClose'),
	editDelete: document.getElementById('userManageEditDelete'),
	editSelect: document.getElementById('userManageEditSelect'),
	editName: document.getElementById('userManageEditName'),
	editPerm: document.getElementById('userManageEditPerm'),
	editPass: document.getElementById('userManageEditPass'),
	editGroups: document.getElementById('userManageEditGroups'),
	editInstances: document.getElementById('userManageEditInstances'),
	editGroupAdd: document.getElementById('userManageEditGroupAdd'),
	editGroupAddBtn: document.getElementById('userManageEditGroupAddBtn'),
	editInstanceAdd: document.getElementById('userManageEditInstanceAdd'),
	editInstanceAddBtn: document.getElementById('userManageEditInstanceAddBtn'),
};

const modalState = {
	closeTimer: null,
	editStatusTimer: null,
	editLoadSeq: 0,
	isBound: false,
	currentPage: 'me',
	meUser: '',
	mePerm: 0,
	isAdmin: false,
	editUser: '',
	editMode: 'edit',
	meLoading: false,
	editLoading: false,
};

const setMeStatus = (text, options = {}) => {
	if (!dom.meStatus) return;
	const msg = String(text || '').trim();
	dom.meStatus.textContent = msg;
	dom.meStatus.classList.toggle('error', !!options.error);
};

const applyPage = (page) => {
	const p = page === 'users' ? 'users' : (page === 'edit' ? 'edit' : 'me');
	modalState.currentPage = p;
	dom.tabMe?.classList.toggle('active', p === 'me');
	dom.tabUsers?.classList.toggle('active', p === 'users');
	dom.tabEdit?.classList.toggle('active', p === 'edit');
	dom.pageMe?.classList.toggle('active', p === 'me');
	dom.pageUsers?.classList.toggle('active', p === 'users');
	dom.pageEdit?.classList.toggle('active', p === 'edit');
};

const applyMeData = (me) => {
	const user = String(me?.user || '').trim();
	const perm = Number.isInteger(me?.perm) ? me.perm : Number(me?.perm || 0);
	modalState.meUser = user;
	modalState.mePerm = Number.isFinite(perm) ? perm : 0;
	modalState.isAdmin = modalState.mePerm === 7;
	if (dom.meName) {
		dom.meName.value = user || '';
	}
	if (dom.tabUsers) {
		dom.tabUsers.classList.toggle('hidden', !modalState.isAdmin);
	}
	if (dom.tabEdit) {
		dom.tabEdit.classList.toggle('hidden', !modalState.isAdmin);
	}
	if (!modalState.isAdmin && modalState.currentPage === 'users') {
		applyPage('me');
	}
	if (!modalState.isAdmin && modalState.currentPage === 'edit') {
		applyPage('me');
	}
};

const updatePassConfirmVisibility = () => {
	const pass = dom.mePass ? String(dom.mePass.value || '') : '';
	const shouldShow = !!pass.trim();
	dom.pageMe?.classList.toggle('show-pass-confirm', shouldShow);
	if (!shouldShow && dom.mePass2) {
		dom.mePass2.value = '';
	}
};

const setEditLoading = (loading) => {
	modalState.editLoading = !!loading;
	if (dom.editSave) dom.editSave.disabled = !!loading;
};

const setMeLoading = (loading) => {
	modalState.meLoading = !!loading;
	if (dom.meSave) dom.meSave.disabled = !!loading;
};

const clearMeFormForLoad = () => {
	if (dom.meName) dom.meName.value = '';
	if (dom.mePass) dom.mePass.value = '';
	if (dom.mePass2) dom.mePass2.value = '';
	updatePassConfirmVisibility();
	setMeStatus('LOADING...');
};

const clearEditFormForLoad = () => {
	if (dom.editName) dom.editName.value = '';
	if (dom.editPass) dom.editPass.value = '';
	if (dom.editPerm) dom.editPerm.value = String(0);
	if (dom.editGroups) dom.editGroups.value = '';
	if (dom.editInstances) dom.editInstances.value = '';
	setEditStatus('LOADING...');
};

const setEditStatus = (text, options = {}) => {
	if (!dom.editStatus) return;
	modalState.editStatusTimer = clearTimer(modalState.editStatusTimer);
	const msg = String(text || '').trim();
	dom.editStatus.textContent = msg;
	dom.editStatus.classList.toggle('error', !!options.error);
};

const flashEditStatus = (text, duration = 1000) => {
	setEditStatus(text);
	modalState.editStatusTimer = setTimeout(() => {
		setEditStatus('');
	}, Math.max(0, Number(duration) || 0));
};

const CREATE_USER_VALUE = '__create__';
const SEP_VALUE = '__sep__';

const createOption = ({ value = '', text = '', disabled = false } = {}) => {
	const option = document.createElement('option');
	option.value = String(value);
	option.textContent = String(text);
	option.disabled = !!disabled;
	return option;
};

const createEmptyState = (text) => {
	const empty = document.createElement('div');
	empty.className = 'user-manage-empty';
	empty.textContent = text;
	return empty;
};

const isCreateMode = () => {
	const v = dom.editSelect ? String(dom.editSelect.value || '').trim() : '';
	return v === CREATE_USER_VALUE;
};

const applyEditModeUI = () => {
	const creating = isCreateMode();
	modalState.editMode = creating ? 'create' : 'edit';
	if (dom.editDelete) {
		dom.editDelete.classList.toggle('hidden', creating);
	}
};

const ensureEditPermOptions = () => {
	if (!dom.editPerm) return;
	if (dom.editPerm.options && dom.editPerm.options.length > 0) {
		return;
	}
	dom.editPerm.replaceChildren(...userPermOptions.map((p) => {
		const value = Number(p);
		return createOption({ value, text: formatUserPerm(value) });
	}));
};

const openEditPageWithSelection = async (selectionValue) => {
	if (!modalState.isAdmin) return;
	ensureEditPermOptions();
	applyPage('edit');
	// Reset to create mode first, then load options.
	modalState.editLoadSeq += 1;
	setEditLoading(true);
	clearEditFormForLoad();
	if (dom.editSelect) {
		dom.editSelect.value = CREATE_USER_VALUE;
	}
	applyEditModeUI();
	await loadEditOptions();
	if (dom.editSelect) {
		const selected = String(selectionValue || CREATE_USER_VALUE);
		dom.editSelect.value = selected === SEP_VALUE ? CREATE_USER_VALUE : selected;
	}
	applyEditModeUI();
	const loaded = await loadEditUser(dom.editSelect?.value);
	setEditLoading(!loaded);
};

const splitLines = (text) => String(text || '')
	.split('\n')
	.map((s) => s.trim())
	.filter(Boolean);

const normalizeScopeLines = (text, maxLength) => {
	const seen = new Set();
	const result = [];
	for (const item of splitLines(text)) {
		const normalized = truncateText(item, maxLength).trim();
		if (!normalized || seen.has(normalized)) continue;
		seen.add(normalized);
		result.push(normalized);
		if (result.length >= USER_SCOPE_MAX_ITEMS) break;
	}
	return result;
};

const normalizeScopeTextarea = (textarea, maxLength) => {
	const value = normalizeScopeLines(textarea?.value || '', maxLength).join('\n');
	if (textarea) {
		textarea.value = value;
	}
	return value;
};

const addLineToTextarea = async (textarea, value, maxLength) => {
	if (!textarea) return;
	const v = truncateText(value, maxLength).trim();
	if (!v) return;
	const items = splitLines(textarea.value);
	if (items.length >= USER_SCOPE_MAX_ITEMS) {
		await showAlert(`每个用户最多选择 ${USER_SCOPE_MAX_ITEMS} 项`, { title: 'INPUT' });
		return;
	}
	if (items.includes(v)) {
		return;
	}
	items.push(v);
	textarea.value = items.join('\n');
};

const loadEditOptions = async () => {
	if (!modalState.isAdmin) return;
	const usersResult = await fetchUsers();
	const users = usersResult.ok ? (usersResult.data?.users || []) : [];
	if (dom.editSelect) {
		const prev = String(dom.editSelect.value || '').trim();
		dom.editSelect.replaceChildren(
			createOption({ value: CREATE_USER_VALUE, text: '+ CREATE USER' }),
			createOption({ value: SEP_VALUE, text: '────────', disabled: true }),
			...users.map((u) => {
			const name = String(u?.user || '').trim();
				return createOption({ value: name, text: name });
			}),
		);
		if (prev) {
			if (prev === SEP_VALUE) {
				return;
			}
			const hasPrev = Array.from(dom.editSelect.options || []).some((opt) => opt?.value === prev);
			if (hasPrev) {
				dom.editSelect.value = prev;
			}
		}
	}
	instanceStatusStore.start();
	const instancesSnapshot = await instanceStatusStore.waitForReady();
	const instances = Array.isArray(instancesSnapshot.instances) ? instancesSnapshot.instances : [];
	const groupSet = new Set();
	instances.forEach((ins) => {
		const g = String(ins?.group || '').trim() || 'UNGROUPED';
		groupSet.add(g);
	});
	const groups = Array.from(groupSet).sort();
	if (dom.editGroupAdd) {
		dom.editGroupAdd.replaceChildren(
			createOption({ value: '', text: '-' }),
			...groups.map((g) => createOption({ value: g, text: g })),
		);
	}
	if (dom.editInstanceAdd) {
		dom.editInstanceAdd.replaceChildren(
			createOption({ value: '', text: '-' }),
			...instances.map((ins) => {
			const name = String(ins?.name || '').trim();
				return createOption({ value: name, text: name });
			}),
		);
	}
};

const loadEditUser = async (username) => {
	if (!modalState.isAdmin) return;
	applyEditModeUI();
	if (String(username || '').trim() === SEP_VALUE) {
		return false;
	}
	if (isCreateMode()) {
		modalState.editLoadSeq += 1;
		setEditLoading(false);
		setEditStatus('');
		if (dom.editName) dom.editName.value = '';
		if (dom.editPass) dom.editPass.value = '';
		if (dom.editPerm) dom.editPerm.value = String(0);
		if (dom.editGroups) dom.editGroups.value = '';
		if (dom.editInstances) dom.editInstances.value = '';
		return true;
	}
	const u = String(username || '').trim();
	if (!u) return false;

	const loadSeq = modalState.editLoadSeq + 1;
	modalState.editLoadSeq = loadSeq;
	setEditLoading(true);
	clearEditFormForLoad();
	let loaded = false;
	try {
		const result = await fetchAdminUser(u);
		if (modalState.editLoadSeq != loadSeq) {
			return false;
		}
		if (!result.ok || !result.data) {
			setEditStatus(result.error || '加载失败', { error: true });
			return false;
		}
		const data = result.data;
		modalState.editUser = String(data.user || u).trim();
		if (dom.editName) dom.editName.value = modalState.editUser;
		if (dom.editPerm) dom.editPerm.value = String(data.perm ?? 0);
		if (dom.editGroups) dom.editGroups.value = normalizeScopeLines((data.allow_groups || []).join('\n'), USER_NAME_MAX_LENGTH).join('\n');
		if (dom.editInstances) dom.editInstances.value = normalizeScopeLines((data.allow_instances || []).join('\n'), USER_NAME_MAX_LENGTH).join('\n');
		if (dom.editPass) dom.editPass.value = '';
		setEditStatus('');
		loaded = true;
		return true;
	} finally {
		if (modalState.editLoadSeq == loadSeq) {
			setEditLoading(!loaded);
		}
	}
};

const saveEditUser = async () => {
	if (modalState.editLoading) return;
	if (!modalState.isAdmin) {
		return;
	}
	const selected = dom.editSelect ? String(dom.editSelect.value || '').trim() : '';
	const newUser = dom.editName ? truncateText(dom.editName.value || '', USER_NAME_MAX_LENGTH).trim() : '';
	const permRaw = dom.editPerm ? String(dom.editPerm.value || '').trim() : '';
	const pass = dom.editPass ? truncateText(dom.editPass.value || '', USER_PASSWORD_MAX_LENGTH) : '';
	const hasPass = !!pass.trim();
	if (!selected) {
		await showAlert('请选择用户', { title: 'INPUT' });
		return;
	}
	if (!newUser) {
		await showAlert('名称不能为空', { title: 'INPUT' });
		dom.editName?.focus();
		return;
	}
	const perm = parseInt(permRaw, 10);
	if (![0, 2, 7].includes(perm)) {
		await showAlert('权限等级无效', { title: 'INPUT' });
		return;
	}
	normalizeScopeTextarea(dom.editGroups, USER_NAME_MAX_LENGTH);
	normalizeScopeTextarea(dom.editInstances, USER_NAME_MAX_LENGTH);
	const allow_groups = normalizeScopeLines(dom.editGroups?.value || '', USER_NAME_MAX_LENGTH);
	const allow_instances = normalizeScopeLines(dom.editInstances?.value || '', USER_NAME_MAX_LENGTH);

	if (selected === CREATE_USER_VALUE) {
		if (!hasPass) {
			await showAlert('密码不能为空', { title: 'INPUT' });
			dom.editPass?.focus();
			return;
		}
		setEditStatus('正在创建...');
		await withActionsDisabled(dom.editActions, async () => {
			const res = await createAdminUser({
				user: newUser,
				pass,
				perm,
				allow_groups,
				allow_instances,
			});
			if (!res.ok || !res.data) {
				await showAlert(res.error || '创建失败', { title: 'ERROR', tone: 'danger' });
				setEditStatus('');
				return;
			}
			const data = res.data;
			flashEditStatus('保存完成', 1000);
			await loadEditOptions();
			if (dom.editSelect) dom.editSelect.value = String(data.user || newUser);
			applyEditModeUI();
			await loadEditUser(dom.editSelect?.value);
		});
		return;
	}

	setEditStatus('正在保存...');
	await withActionsDisabled(dom.editActions, async () => {
		const res = await updateAdminUser({
			user: selected,
			new_user: newUser,
			pass: hasPass ? pass : '',
			perm,
			allow_groups,
			allow_instances,
		});
		if (!res.ok || !res.data) {
			await showAlert(res.error || '保存失败', { title: 'ERROR', tone: 'danger' });
			setEditStatus('');
			return;
		}
		const data = res.data;
		await loadEditOptions();
		if (dom.editSelect) dom.editSelect.value = String(data.user || newUser);
		applyEditModeUI();
		await loadEditUser(dom.editSelect?.value);
		flashEditStatus('保存完成', 1000);
	});
};

const deleteEditUser = async () => {
	if (!modalState.isAdmin) return;
	const target = dom.editSelect ? String(dom.editSelect.value || '').trim() : '';
	if (!target || target === CREATE_USER_VALUE) {
		return;
	}
	const ok = await showConfirm(`确认删除用户: ${target} ?`, {
		title: 'DELETE USER',
		okText: 'DELETE',
		cancelText: 'CANCEL',
		tone: 'danger',
	});
	if (!ok) return;
	setEditStatus('正在删除...');
	await withActionsDisabled(dom.editActions, async () => {
		const res = await deleteAdminUser(target);
		if (!res.ok) {
			await showAlert(res.error || '删除失败', { title: 'ERROR', tone: 'danger' });
			setEditStatus('');
			return;
		}
		setEditStatus('已删除');
		await loadEditOptions();
		if (dom.editSelect) {
			dom.editSelect.value = CREATE_USER_VALUE;
		}
		applyEditModeUI();
		await loadEditUser(dom.editSelect?.value);
	});
};

const renderUsers = (users) => {
	if (!dom.list) return;
	if (!users || !Array.isArray(users) || users.length === 0) {
		dom.list.replaceChildren(createEmptyState('EMPTY'));
		return;
	}

	const table = document.createElement('table');
	table.className = 'user-manage-table';

	const thead = document.createElement('thead');
	const headRow = document.createElement('tr');
	['USER', 'PERM', 'SCOPE'].forEach((label) => {
		const th = document.createElement('th');
		th.setAttribute('align', 'left');
		th.textContent = label;
		headRow.appendChild(th);
	});
	thead.appendChild(headRow);

	const tbody = document.createElement('tbody');
	users.forEach((u) => {
		const name = String(u?.user || '').trim();
		const perm = Number.isInteger(u?.perm) ? u.perm : Number(u?.perm || 0);
		const insCnt = Number.isInteger(u?.allow_instances_cnt) ? u.allow_instances_cnt : Number(u?.allow_instances_cnt || 0);
		const grpCnt = Number.isInteger(u?.allow_groups_cnt) ? u.allow_groups_cnt : Number(u?.allow_groups_cnt || 0);
		const safeInsCnt = Number.isFinite(insCnt) ? insCnt : 0;
		const safeGrpCnt = Number.isFinite(grpCnt) ? grpCnt : 0;
		const scopeText = `${safeInsCnt} ins / ${safeGrpCnt} grp`;

		const tr = document.createElement('tr');
		tr.dataset.user = name || '';

		const nameCell = document.createElement('td');
		nameCell.textContent = name || '-';

		const permCell = document.createElement('td');
		permCell.textContent = formatUserPerm(perm);

		const scopeCell = document.createElement('td');
		scopeCell.textContent = scopeText;

		tr.append(nameCell, permCell, scopeCell);
		tbody.appendChild(tr);
	});

	table.append(thead, tbody);
	dom.list.replaceChildren(table);
};

const setLoading = (loading) => {
	if (!dom.list) return;
	dom.list.classList.toggle('loading', !!loading);
	if (loading) {
		dom.list.replaceChildren(createEmptyState('LOADING...'));
	}
};

const loadMe = async () => {
	setMeLoading(true);
	clearMeFormForLoad();
	const meResult = await fetchMe();
	if (meResult.ok && meResult.data) {
		applyMeData(meResult.data);
		setMeStatus('');
		setMeLoading(false);
	} else {
		setMeStatus(meResult.error || '加载当前用户失败', { error: true });
		setMeLoading(true);
	}
	if (dom.mePass) {
		dom.mePass.value = '';
	}
	if (dom.mePass2) {
		dom.mePass2.value = '';
	}
	updatePassConfirmVisibility();
	return !!(meResult.ok && meResult.data);
};

const loadUsers = async () => {
	if (!modalState.isAdmin) {
		return;
	}
	await withActionsDisabled(dom.actions, async () => {
		setLoading(true);
		try {
			const result = await fetchUsers();
			if (!result.ok) {
				renderUsers([]);
				setEditStatus(result.error || '加载用户列表失败', { error: true });
				return;
			}
			const list = result.data?.users || [];
			renderUsers(list);
		} finally {
			setLoading(false);
		}
	});
};

const saveMe = async () => {
	if (modalState.meLoading) return;
	const name = dom.meName ? truncateText(dom.meName.value || '', USER_NAME_MAX_LENGTH).trim() : '';
	const pass = dom.mePass ? truncateText(dom.mePass.value || '', USER_PASSWORD_MAX_LENGTH) : '';
	const pass2 = dom.mePass2 ? truncateText(dom.mePass2.value || '', USER_PASSWORD_MAX_LENGTH) : '';
	const hasPass = !!pass.trim();

	if (!name) {
		await showAlert('名称不能为空', { title: 'INPUT' });
		dom.meName?.focus();
		return;
	}
	if (hasPass && pass !== pass2) {
		await showAlert('两次密码不一致', { title: 'INPUT' });
		dom.mePass2?.focus();
		return;
	}
	if (name === (modalState.meUser || '') && !hasPass) {
		await showAlert('未填写修改内容', { title: 'INPUT' });
		return;
	}

	setMeStatus('SAVING...');
	await withActionsDisabled(dom.meActions, async () => {
		const res = await updateMe({ name, pass: hasPass ? pass : '' });
		if (!res.ok || !res.data) {
			setMeStatus(res.error || '保存失败', { error: true });
			return;
		}
		setMeStatus('SAVED');
		applyMeData(res.data);
		if (dom.mePass) dom.mePass.value = '';
		if (dom.mePass2) dom.mePass2.value = '';
		updatePassConfirmVisibility();
	});
};

const open = async () => {
	if (!dom.modal) return;
	modalState.closeTimer = clearTimer(modalState.closeTimer);
	dom.modal.style.display = 'flex';
	dom.modal.classList.remove('closing');
	requestAnimationFrame(() => {
		dom.modal.classList.add('visible');
		dom.meName?.focus();
	});
	applyPage('me');
	await loadMe();
};

const close = () => {
	if (!dom.modal) return;
	dom.modal.classList.remove('visible');
	dom.modal.classList.add('closing');
	modalState.closeTimer = setTimeout(() => {
		dom.modal.style.display = 'none';
		dom.modal.classList.remove('closing');
		modalState.closeTimer = null;
	}, 280);
};

const bindEvents = () => {
	if (modalState.isBound) return;
	modalState.isBound = true;

	if (dom.close) {
		dom.close.onclick = () => close();
	}
	if (dom.cancel) {
		dom.cancel.onclick = () => close();
	}
	if (dom.cancel2) {
		dom.cancel2.onclick = () => close();
	}
	if (dom.tabMe) {
	dom.tabMe.onclick = () => {
			applyPage('me');
			void loadMe();
		};
	}
	if (dom.tabUsers) {
		dom.tabUsers.onclick = () => {
			if (!modalState.isAdmin) {
				return;
			}
			applyPage('users');
			loadUsers();
		};
	}
	if (dom.tabEdit) {
		dom.tabEdit.onclick = async () => {
			if (!modalState.isAdmin) {
				return;
			}
			await openEditPageWithSelection(dom.editSelect?.value);
		};
	}
	if (dom.pageMe) {
		dom.pageMe.onsubmit = async (event) => {
			event.preventDefault();
			await saveMe();
		};
	}
	if (dom.mePass) {
		dom.mePass.oninput = () => updatePassConfirmVisibility();
	}
	if (dom.editGroups) {
		dom.editGroups.oninput = () => {
			const items = splitLines(dom.editGroups.value);
			if (items.length >= USER_SCOPE_MAX_ITEMS && /\n$/.test(dom.editGroups.value)) {
				void showAlert(`每个用户最多选择 ${USER_SCOPE_MAX_ITEMS} 个组`, { title: 'INPUT' });
			}
		};
		dom.editGroups.onblur = () => normalizeScopeTextarea(dom.editGroups, USER_NAME_MAX_LENGTH);
	}
	if (dom.editInstances) {
		dom.editInstances.oninput = () => {
			const items = splitLines(dom.editInstances.value);
			if (items.length >= USER_SCOPE_MAX_ITEMS && /\n$/.test(dom.editInstances.value)) {
				void showAlert(`每个用户最多选择 ${USER_SCOPE_MAX_ITEMS} 个实例`, { title: 'INPUT' });
			}
		};
		dom.editInstances.onblur = () => normalizeScopeTextarea(dom.editInstances, USER_NAME_MAX_LENGTH);
	}
	if (dom.editClose) {
		dom.editClose.onclick = () => close();
	}
	if (dom.editSelect) {
	dom.editSelect.onchange = () => {
			applyEditModeUI();
			void loadEditUser(dom.editSelect.value);
		};
	}
	if (dom.pageEdit) {
		dom.pageEdit.onsubmit = async (event) => {
			event.preventDefault();
			await saveEditUser();
		};
	}
	if (dom.editGroupAddBtn) {
	dom.editGroupAddBtn.onclick = async () => {
			const v = dom.editGroupAdd ? String(dom.editGroupAdd.value || '').trim() : '';
			await addLineToTextarea(dom.editGroups, v, USER_NAME_MAX_LENGTH);
		};
	}
	if (dom.editInstanceAddBtn) {
		dom.editInstanceAddBtn.onclick = async () => {
			const v = dom.editInstanceAdd ? String(dom.editInstanceAdd.value || '').trim() : '';
			await addLineToTextarea(dom.editInstances, v, USER_NAME_MAX_LENGTH);
		};
	}
	if (dom.editDelete) {
		dom.editDelete.onclick = async () => {
			await deleteEditUser();
		};
	}
	if (dom.list) {
		dom.list.onclick = (event) => {
			if (!modalState.isAdmin) {
				return;
			}
			const th = event.target?.closest?.('thead');
			if (th) {
				openEditPageWithSelection(CREATE_USER_VALUE);
				return;
			}
			const tr = event.target?.closest?.('tbody tr[data-user]');
			const user = tr?.getAttribute?.('data-user') || '';
			if (!user) {
				return;
			}
			openEditPageWithSelection(user);
		};
	}
	if (dom.logout) {
		dom.logout.onclick = async () => {
			const ok = await showConfirm('退出登录并返回登录页面?', {
				title: 'LOGOUT',
				okText: 'LOGOUT',
				cancelText: 'CANCEL',
			});
			if (!ok) {
				return;
			}
			setMeStatus('LOGGING OUT...');
			await withActionsDisabled(dom.meActions, async () => {
				const res = await logout();
				if (!res?.ok) {
					setMeStatus(res?.message || 'Logout failed', { error: true });
					return;
				}
				clearAllStoredDataAndEnterUnauthorizedState();
			});
		};
	}
	if (dom.resetToken) {
		dom.resetToken.onclick = async () => {
			const ok = await showConfirm('服务端将重新生成 Token, 并断开当前用户的所有连接', {
				title: 'RESET TOKEN',
				okText: 'RESET',
				cancelText: 'CANCEL',
				tone: 'danger',
			});
			if (!ok) {
				return;
			}
			setMeStatus('RESETTING...');
			await withActionsDisabled(dom.meActions, async () => {
				const res = await resetToken();
				clearAllStoredData();
				if (!res?.ok) {
					setMeStatus(res?.message || '重置失败', { error: true });
					return;
				}
				dispatchUnauthorized();
			});
		};
	}
};

export const bootUserManageModal = () => {
	bindEvents();
	return {
		open,
		close,
	};
};
