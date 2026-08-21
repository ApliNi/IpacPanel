import { mainModalOverlay } from "../ui.js";
import { clearTimer, withActionsDisabled } from '../utils/utils.js';
import { updateGroup } from '../api/instance.js';
import { showAlert, showConfirm } from './dialog.js';

const createFieldGroup = ({ label, className = '', content } = {}) => {
	const group = document.createElement('div');
	group.className = ['field-group', className].filter(Boolean).join(' ');
	if (label !== undefined) {
		const labelEl = document.createElement('span');
		labelEl.textContent = String(label || '');
		group.appendChild(labelEl);
	}
	if (content instanceof Node) {
		group.appendChild(content);
	}
	return group;
};

const createModalActions = ({ id = '', className = '', content = [] } = {}) => {
	const actions = document.createElement('div');
	actions.className = ['modal-actions', className].filter(Boolean).join(' ');
	if (id) {
		actions.id = id;
	}
	actions.append(...content);
	return actions;
};

const createActionsGroup = ({ content = [] } = {}) => {
	const group = document.createElement('div');
	group.className = 'modal-actions-group';
	group.append(...content);
	return group;
};

const modal = document.createElement('div');
modal.id = 'instanceGroupModal';
modal.className = 'modal-overlay';

const card = document.createElement('div');
card.className = 'modal-card instance-group-modal-card';

const header = document.createElement('div');
header.className = 'modal-header';

const title = document.createElement('span');
title.className = 'modal-title';
title.textContent = 'GROUP';

const closeButton = document.createElement('button');
closeButton.id = 'instanceGroupClose';
closeButton.className = 'modal-close';
closeButton.type = 'button';
closeButton.textContent = '×';

header.append(title, closeButton);

const formWrap = document.createElement('div');
formWrap.className = 'modal-form instance-group-form';

const tabs = document.createElement('div');
tabs.className = 'filter-group instance-group-tabs';

const tabRename = document.createElement('button');
tabRename.id = 'instanceGroupTabRename';
tabRename.className = 'filter-btn active';
tabRename.type = 'button';
tabRename.dataset.page = 'rename';
tabRename.textContent = 'RENAME';

const tabDelete = document.createElement('button');
tabDelete.id = 'instanceGroupTabDelete';
tabDelete.className = 'filter-btn';
tabDelete.type = 'button';
tabDelete.dataset.page = 'delete';
tabDelete.textContent = 'DELETE';

tabs.append(tabRename, tabDelete);

const body = document.createElement('div');
body.className = 'instance-group-body';

const renameForm = document.createElement('form');
renameForm.id = 'instanceGroupPageRename';
renameForm.className = 'instance-group-page active';

const nameInput = document.createElement('input');
nameInput.id = 'instanceGroupName';
nameInput.type = 'text';
nameInput.maxLength = 32;
nameInput.autocomplete = 'off';
nameInput.required = true;

const countText = document.createElement('div');
countText.id = 'instanceGroupCount';
countText.className = 'file-action-static';

const statusText = document.createElement('span');
statusText.id = 'instanceGroupStatus';
statusText.ariaLive = 'polite';

const newInstanceButton = document.createElement('button');
newInstanceButton.id = 'instanceGroupCreateInstance';
newInstanceButton.className = 'btn';
newInstanceButton.type = 'button';
newInstanceButton.textContent = 'NEW INSTANCE';

const cancelButton = document.createElement('button');
cancelButton.id = 'instanceGroupCancel';
cancelButton.className = 'btn';
cancelButton.type = 'button';
cancelButton.textContent = 'CANCEL';

const saveButton = document.createElement('button');
saveButton.className = 'btn btn-start';
saveButton.type = 'submit';
saveButton.textContent = 'SAVE';

renameForm.append(
	createFieldGroup({ label: 'GROUP', content: nameInput }),
	createFieldGroup({ label: 'COUNT', content: countText }),
	createModalActions({
		id: 'instanceGroupRenameActions',
		content: [createActionsGroup({ content: [statusText, newInstanceButton, cancelButton, saveButton] })],
	}),
);

const deleteForm = document.createElement('form');
deleteForm.id = 'instanceGroupPageDelete';
deleteForm.className = 'instance-group-page';

const deleteWarning = document.createElement('div');
deleteWarning.className = 'file-action-static file-delete-warning';
deleteWarning.textContent = '删除该组后, 实例将处于未分组状态';

const deleteName = document.createElement('div');
deleteName.id = 'instanceGroupDeleteName';
deleteName.className = 'file-action-static';

const deleteStatusText = document.createElement('span');
deleteStatusText.id = 'instanceGroupDeleteStatus';
deleteStatusText.ariaLive = 'polite';

const deleteCancelButton = document.createElement('button');
deleteCancelButton.id = 'instanceGroupDeleteCancel';
deleteCancelButton.className = 'btn';
deleteCancelButton.type = 'button';
deleteCancelButton.textContent = 'CANCEL';

const deleteButton = document.createElement('button');
deleteButton.className = 'btn';
deleteButton.type = 'submit';
deleteButton.textContent = 'DELETE';

deleteForm.append(
	createFieldGroup({ label: 'WARNING', content: deleteWarning }),
	createFieldGroup({ label: 'GROUP', content: deleteName }),
	createModalActions({
		id: 'instanceGroupDeleteActions',
		content: [deleteStatusText, deleteCancelButton, deleteButton],
	}),
);

body.append(renameForm, deleteForm);
formWrap.append(tabs, body);
card.append(header, formWrap);
modal.appendChild(card);
mainModalOverlay.appendChild(modal);

const dom = {
    modal: document.getElementById('instanceGroupModal'),
    close: document.getElementById('instanceGroupClose'),
    tabRename: document.getElementById('instanceGroupTabRename'),
    tabDelete: document.getElementById('instanceGroupTabDelete'),
    pageRename: document.getElementById('instanceGroupPageRename'),
    pageDelete: document.getElementById('instanceGroupPageDelete'),
    nameInput: document.getElementById('instanceGroupName'),
	countText: document.getElementById('instanceGroupCount'),
	deleteName: document.getElementById('instanceGroupDeleteName'),
	createInstance: document.getElementById('instanceGroupCreateInstance'),
	cancel: document.getElementById('instanceGroupCancel'),
    deleteCancel: document.getElementById('instanceGroupDeleteCancel'),
    renameActions: document.getElementById('instanceGroupRenameActions'),
    deleteActions: document.getElementById('instanceGroupDeleteActions'),
    status: document.getElementById('instanceGroupStatus'),
    deleteStatus: document.getElementById('instanceGroupDeleteStatus'),
};

const modalState = {
	closeTimer: null,
	currentPage: 'rename',
	groupName: '',
	createGroupName: '',
	instances: [],
	existingGroups: [],
	onReload: null,
	onCreateInstance: null,
	isBound: false,
};

const isDuplicateGroupName = (name) => {
	const typed = String(name || '').trim();
	if (!typed) return false;
	const list = Array.isArray(modalState.existingGroups) ? modalState.existingGroups : [];
	for (let i = 0; i < list.length; i += 1) {
		const g = String(list[i] || '').trim();
		if (!g) continue;
		if (g === typed) {
			return true;
		}
	}
	return false;
};

const setStatus = (text, options = {}) => {
    if (dom.status) {
        dom.status.textContent = String(text || '').trim();
        dom.status.classList.toggle('error', !!options.error);
    }
};

const setDeleteStatus = (text, options = {}) => {
    if (dom.deleteStatus) {
        dom.deleteStatus.textContent = String(text || '').trim();
        dom.deleteStatus.classList.toggle('error', !!options.error);
    }
};

const applyPage = (page) => {
    const p = page === 'delete' ? 'delete' : 'rename';
    modalState.currentPage = p;
    dom.tabRename.classList.toggle('active', p === 'rename');
    dom.tabDelete.classList.toggle('active', p === 'delete');
    dom.pageRename.classList.toggle('active', p === 'rename');
    dom.pageDelete.classList.toggle('active', p === 'delete');
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

const ensureGroupHasInstances = async () => {
	const targets = Array.isArray(modalState.instances) ? modalState.instances : [];
	const list = targets.filter((ins) => String(ins?.name || '').trim());
	if (list.length === 0) {
		await showAlert('当前组没有实例', { title: 'NOTICE' });
		return false;
	}
	return true;
};

const openCreateInstanceForGroup = async () => {
	if (typeof modalState.onCreateInstance !== 'function') {
		await showAlert('创建实例操作不可用', { title: 'ERROR', tone: 'danger' });
		return;
	}
	close();
	modalState.onCreateInstance({ group: modalState.createGroupName });
};

const submitRename = async () => {
	const newName = String(dom.nameInput.value || '').trim();
	if (!newName) {
		await showAlert('组名是必须项', { title: 'INPUT' });
		dom.nameInput.focus();
		return;
	}
	if (newName.length > 32) {
		await showAlert('组名不得超过32个字符', { title: 'INPUT' });
		dom.nameInput.focus();
		return;
	}
	if (newName === modalState.groupName) {
		close();
		return;
	}

	if (isDuplicateGroupName(newName)) {
		const ok = await showConfirm('该组已存在, 实例将被合并. 是否继续?', {
			title: 'CONFIRM',
			okText: 'CONTINUE',
			cancelText: 'CANCEL',
			tone: 'warning',
		});
		if (!ok) {
			dom.nameInput.focus();
			return;
		}
	}

	if (!(await ensureGroupHasInstances())) {
		return;
	}

	setStatus('Saving...');
	const result = await updateGroup(modalState.groupName, newName);
	if (!result.ok) {
		if (result.unauthorized) {
			return;
		}
		await showAlert(result.error || '保存组失败', { title: 'ERROR', tone: 'danger' });
		setStatus(result.error || '保存组失败', { error: true });
		return;
	}
	setStatus('Saved');
	close();
	if (typeof modalState.onReload === 'function') {
		await modalState.onReload();
	}
};

const submitDelete = async () => {
	if (!(await ensureGroupHasInstances())) {
		return;
	}

	setDeleteStatus('Processing...');
	const result = await updateGroup(modalState.groupName, 'UNGROUPED');
	if (!result.ok) {
		if (result.unauthorized) {
			return;
		}
		await showAlert(result.error || '删除组失败', { title: 'ERROR', tone: 'danger' });
		setDeleteStatus(result.error || '删除组失败', { error: true });
		return;
	}
	setDeleteStatus('Done');
	close();
	if (typeof modalState.onReload === 'function') {
		await modalState.onReload();
	}
};

const open = ({ groupName, createGroupName, instances, existingGroups }) => {
	if (!dom.modal) return;
    modalState.closeTimer = clearTimer(modalState.closeTimer);
	modalState.groupName = String(groupName || '').trim();
	modalState.createGroupName = String(createGroupName ?? groupName ?? '').trim();
	modalState.instances = Array.isArray(instances) ? instances : [];
	modalState.existingGroups = Array.isArray(existingGroups) ? existingGroups : [];
	setStatus('');
	setDeleteStatus('');

    if (dom.nameInput) {
        dom.nameInput.value = modalState.groupName;
    }
    if (dom.countText) {
        dom.countText.textContent = String(modalState.instances.length);
    }
    if (dom.deleteName) {
        dom.deleteName.textContent = modalState.groupName || '-';
    }
    applyPage('rename');

    dom.modal.style.display = 'flex';
    dom.modal.classList.remove('closing');
    requestAnimationFrame(() => {
        dom.modal.classList.add('visible');
        dom.nameInput.focus();
        if (dom.nameInput.setSelectionRange) {
            const len = dom.nameInput.value.length;
            dom.nameInput.setSelectionRange(len, len);
        }
    });
};

const bindEvents = () => {
    if (modalState.isBound) return;
    modalState.isBound = true;

    dom.close.addEventListener('click', () => close());
    dom.cancel.addEventListener('click', () => close());
    dom.deleteCancel.addEventListener('click', () => close());
    dom.createInstance.addEventListener('click', () => void openCreateInstanceForGroup());
    dom.tabRename.addEventListener('click', () => applyPage('rename'));
    dom.tabDelete.addEventListener('click', () => applyPage('delete'));

    dom.pageRename.addEventListener('submit', async (event) => {
        event.preventDefault();
        await withActionsDisabled(dom.renameActions, submitRename);
    });
    dom.pageDelete.addEventListener('submit', async (event) => {
        event.preventDefault();
        await withActionsDisabled(dom.deleteActions, submitDelete);
    });
};

export const bootInstanceGroupModal = (options = {}) => {
    modalState.onReload = options.onReload || null;
	modalState.onCreateInstance = typeof options.onCreateInstance === 'function' ? options.onCreateInstance : null;
    bindEvents();
    return {
        open,
        close,
    };
};
