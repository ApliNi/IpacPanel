import { closeAnimatedModal, openAnimatedModal, syncToggleButtons } from '../utils/utils.js';

export const applyInstanceModalPageState = ({ dom, pageState, modalState, page }) => {
	pageState.instanceModalPage = page || 'basic';
	const isBasic = page === 'basic';
	const isAdvanced = page === 'advanced';
	const isTasks = page === 'tasks';
	const isDelete = page === 'delete';
	const editingName = modalState?.editingInstanceName || '';

	dom.instanceModalPageBasic.classList.toggle('active', isBasic);
	dom.instanceModalPageAdvanced.classList.toggle('active', isAdvanced);
	dom.instanceModalPageTasks.classList.toggle('active', isTasks);
	dom.instanceModalPageDelete.classList.toggle('active', isDelete);

	if (dom.instanceDeleteName) {
		dom.instanceDeleteName.disabled = !isDelete;
		dom.instanceDeleteName.required = isDelete;
	}

	syncToggleButtons(dom.instanceModalTabs, page);

	if (dom.instanceModalSubmit) {
		if (isDelete) {
			dom.instanceModalSubmit.innerText = 'DELETE';
			dom.instanceModalSubmit.classList.remove('btn-start');
		} else {
			dom.instanceModalSubmit.classList.add('btn-start');
			dom.instanceModalSubmit.innerText = pageState.instanceModalMode === 'edit' ? 'SAVE' : 'CREATE';
		}
	}

	if (dom.instanceDeleteHintName) {
		dom.instanceDeleteHintName.textContent = editingName ? `${editingName}` : '';
		dom.instanceDeleteHintName.classList.toggle('hidden', !editingName);
	}
};

export const bindInstanceModalViewEvents = ({ dom, onSwitchPage, onOpenHelp }) => {
	if (dom.instanceModalTabs.length) {
		dom.instanceModalTabs.forEach((btn) => {
			btn.onclick = () => onSwitchPage?.(btn.dataset.page);
		});
	}
	if (dom.instanceTaskHelpToggle) {
		dom.instanceTaskHelpToggle.onclick = () => onOpenHelp?.();
	}
	if (dom.instanceDeleteHintName) {
		dom.instanceDeleteHintName.onclick = () => {
			if (dom.instanceDeleteHintName.classList.contains('hidden')) {
				return;
			}
			const selection = window.getSelection?.();
			if (!selection) {
				return;
			}
			selection.removeAllRanges();
			const range = document.createRange();
			range.selectNodeContents(dom.instanceDeleteHintName);
			selection.addRange(range);
		};
	}
};

export const openInstanceHelpModal = (modal) => {
	if (!modal) {
		return;
	}
	openAnimatedModal(modal);
};

export const closeInstanceHelpModal = (modal, timer, onClosed) => {
	if (!modal || modal.style.display === 'none') {
		return timer;
	}
	return closeAnimatedModal(modal, timer, onClosed);
};
