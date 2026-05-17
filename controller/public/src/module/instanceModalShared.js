import { InputValidation } from '../utils/inputValidation.js';
import { normalizeTerminalMode, terminalMode } from '../utils/enum.js';

export const NEW_GROUP_VALUE = ':new';
export const NONE_GROUP_VALUE = '';
export const GROUP_SEPARATOR_VALUE = ':sep';
export const DEFAULT_TERMINAL_ENCODING = 'utf-8';
export const TERMINAL_ENCODING_OPTIONS = [
	{ value: 'utf-8', label: 'UTF-8' },
	{ value: 'gbk', label: 'GBK / CP936' },
	{ value: 'gb18030', label: 'GB18030' },
	{ value: 'big5', label: 'Big5 / CP950' },
	{ value: 'shift_jis', label: 'Shift_JIS / CP932' },
	{ value: 'euc-jp', label: 'EUC-JP' },
	{ value: 'iso-2022-jp', label: 'ISO-2022-JP' },
	{ value: 'euc-kr', label: 'EUC-KR / CP949' },
	{ value: 'windows-1252', label: 'Windows-1252' },
	{ value: 'windows-1251', label: 'Windows-1251' },
	{ value: 'windows-1250', label: 'Windows-1250' },
	{ value: 'iso-8859-1', label: 'ISO-8859-1 / Latin-1' },
	{ value: 'utf-16le', label: 'UTF-16LE' },
	{ value: 'utf-16be', label: 'UTF-16BE' },
];

export const normalizeEncodingValue = (value) => {
	const current = String(value || '').trim().toLowerCase();
	if (!current || current === 'utf8') {
		return DEFAULT_TERMINAL_ENCODING;
	}
	const matched = TERMINAL_ENCODING_OPTIONS.find(item => item.value === current);
	return matched ? matched.value : DEFAULT_TERMINAL_ENCODING;
};

const createOption = ({ value = '', text = '', disabled = false, selected = false } = {}) => {
	const node = document.createElement('option');
	node.value = value;
	node.textContent = text;
	node.disabled = !!disabled;
	node.selected = !!selected;
	return node;
};

export const renderEncodingOptions = (select, selectedValue) => {
	if (!select) {
		return;
	}
	const current = normalizeEncodingValue(selectedValue);
	select.replaceChildren(...TERMINAL_ENCODING_OPTIONS.map((item) => createOption({
		value: item.value,
		text: item.label,
		selected: item.value === current,
	})));
};

export const getExistingGroups = (instances = []) => {
	const set = new Set();
	(instances || []).forEach(ins => {
		const g = String(ins.group || '').trim();
		if (g) {
			set.add(g);
		}
	});
	const list = Array.from(set);
	list.sort((a, b) => a.localeCompare(b));
	return list;
};

export const renderGroupSelectOptions = ({ select, selectedGroup, groups = [] }) => {
	if (!select) {
		return;
	}
	const current = String(selectedGroup || '').trim();
	const nextGroups = Array.isArray(groups) ? [...groups] : [];
	if (current && !nextGroups.includes(current)) {
		nextGroups.unshift(current);
	}

	const children = [
		createOption({ value: NONE_GROUP_VALUE, text: '- UNGROUPED' }),
		createOption({ value: NEW_GROUP_VALUE, text: '+ NEW GROUP' }),
	];

	if (nextGroups.length > 0) {
		children.push(createOption({ value: GROUP_SEPARATOR_VALUE, text: '────────', disabled: true }));
	}

	nextGroups.forEach(g => {
		children.push(createOption({ value: g, text: g }));
	});
	select.replaceChildren(...children);

	select.value = current || NONE_GROUP_VALUE;
};

export const toggleNewGroupInput = (input, show) => {
	if (input) {
		input.classList.toggle('hidden', !show);
	}
	if (!show && input) {
		input.value = '';
	}
};

export const buildTaskRow = (task = {}) => {
	const name = task.name || '';
	const enabled = task.enabled !== false;
	const expr = task.expr || '';
	const legacyStrictRestart = task.action === 'strict_restart';
	const action = legacyStrictRestart ? 'restart' : (task.action || 'start');
	const command = task.command || '';
	const useKillStop = task.use_kill_stop === true;
	const strictRestart = task.strict_restart === true || legacyStrictRestart;
	const showCommand = action === 'command';
	const showStopOptions = action === 'stop' || action === 'restart';
	const showStrictRestart = action === 'restart';

	const enabledInput = document.createElement('input');
	enabledInput.className = 'instance-task-enabled';
	enabledInput.type = 'checkbox';

	const nameInput = document.createElement('input');
	nameInput.className = 'instance-task-name instance-task-input';
	nameInput.type = 'text';
	nameInput.maxLength = InputValidation.limits.taskName;
	nameInput.autocomplete = 'off';
	nameInput.placeholder = ' Task name';

	const exprInput = document.createElement('input');
	exprInput.className = 'instance-task-expr instance-task-input';
	exprInput.type = 'text';
	exprInput.maxLength = InputValidation.limits.taskExpr;
	exprInput.autocomplete = 'off';
	exprInput.placeholder = ' Quartz expr 5 / 6 / 7';

	const actionSelect = document.createElement('select');
	actionSelect.className = 'instance-task-action instance-task-select';
	actionSelect.append(
		createOption({ value: 'start', text: 'START' }),
		createOption({ value: 'stop', text: 'STOP' }),
		createOption({ value: 'restart', text: 'RESTART' }),
		createOption({ value: 'command', text: 'COMMAND' }),
	);

	const cmdInput = document.createElement('input');
	cmdInput.className = 'instance-task-command instance-task-input';
	cmdInput.type = 'text';
	cmdInput.maxLength = InputValidation.limits.taskCommand;
	cmdInput.autocomplete = 'off';
	cmdInput.placeholder = ' ^C / stop';

	const useKillStopLabel = document.createElement('label');
	useKillStopLabel.className = 'checkbox-group instance-task-use-kill-stop';
	useKillStopLabel.title = 'Use KILL mode when stopping the instance';

	const useKillStopInput = document.createElement('input');
	useKillStopInput.className = 'instance-task-use-kill-stop-input';
	useKillStopInput.type = 'checkbox';

	const useKillStopText = document.createElement('span');
	useKillStopText.textContent = 'USE KILL STOP';
	useKillStopLabel.append(useKillStopInput, useKillStopText);

	const strictRestartLabel = document.createElement('label');
	strictRestartLabel.className = 'checkbox-group instance-task-strict-restart';
	strictRestartLabel.title = 'Only restart when instance is running';

	const strictRestartInput = document.createElement('input');
	strictRestartInput.className = 'instance-task-strict-restart-input';
	strictRestartInput.type = 'checkbox';

	const strictRestartText = document.createElement('span');
	strictRestartText.textContent = 'STRICT RESTART';
	strictRestartLabel.append(strictRestartInput, strictRestartText);

	const stopOptions = document.createElement('div');
	stopOptions.className = 'instance-task-restart-options';
	stopOptions.append(useKillStopLabel, strictRestartLabel);

	const delBtn = document.createElement('button');
	delBtn.className = 'modal-close instance-task-delete';
	delBtn.type = 'button';
	delBtn.textContent = '×';

	const row = document.createElement('div');
	row.className = 'instance-task-row';
	row.dataset.taskName = name;

	const actions = document.createElement('div');
	actions.className = 'instance-task-actions';

	const label = document.createElement('label');
	label.className = 'checkbox-group';
	label.title = 'enabled';

	const labelText = document.createElement('span');
	labelText.textContent = 'ON';
	label.append(enabledInput, labelText);
	actions.appendChild(label);

	const main = document.createElement('div');
	main.className = 'instance-task-main';

	const meta = document.createElement('div');
	meta.className = 'instance-task-meta';
	meta.append(nameInput, delBtn);

	const fields = document.createElement('div');
	fields.className = 'instance-task-fields';

	const selectWrapper = document.createElement('div');
	selectWrapper.className = 'select-wrapper';
	selectWrapper.appendChild(actionSelect);
	fields.append(exprInput, selectWrapper);

	main.append(meta, fields, stopOptions, cmdInput);
	row.append(actions, main);
	if (enabledInput) {
		enabledInput.checked = enabled;
	}
	if (nameInput) {
		nameInput.value = name;
	}
	if (exprInput) {
		exprInput.value = expr;
	}
	if (actionSelect) {
		actionSelect.value = action;
	}
	if (cmdInput) {
		cmdInput.value = command;
		cmdInput.classList.toggle('hidden', !showCommand);
	}
	if (useKillStopInput) {
		useKillStopInput.checked = useKillStop;
	}
	if (strictRestartInput) {
		strictRestartInput.checked = strictRestart;
	}
	if (strictRestartLabel) {
		strictRestartLabel.classList.toggle('hidden', !showStrictRestart);
	}
	if (stopOptions) {
		stopOptions.classList.toggle('hidden', !showStopOptions);
	}
	if (actionSelect && cmdInput && stopOptions) {
		actionSelect.onchange = () => {
			const shouldShow = actionSelect.value === 'command';
			const shouldShowStopOptions = actionSelect.value === 'stop' || actionSelect.value === 'restart';
			const shouldShowStrictRestart = actionSelect.value === 'restart';
			cmdInput.classList.toggle('hidden', !shouldShow);
			stopOptions.classList.toggle('hidden', !shouldShowStopOptions);
			strictRestartLabel.classList.toggle('hidden', !shouldShowStrictRestart);
		};
	}

	if (delBtn) {
		delBtn.onclick = () => row.remove();
	}

	return row;
};

export const renderInstanceTasks = (listEl, tasks) => {
	if (!listEl) return;
	listEl.replaceChildren(...(tasks || []).slice(0, InputValidation.limits.tasksPerInstance).map((t) => buildTaskRow(t)));
};

export const getUniqueTaskName = (listEl) => {
	const existing = new Set();
	if (listEl) {
		Array.from(listEl.querySelectorAll('.instance-task-name')).forEach(input => {
			const v = (input.value || '').trim();
			if (v) existing.add(v);
		});
	}
	for (let i = 1; i < 1000; i++) {
		const candidate = `Task-${i}`;
		if (!existing.has(candidate)) return candidate;
	}
	return `Task-${Date.now()}`;
};

export const collectInstanceTasks = (listEl) => {
	if (!listEl) return [];
	const rows = Array.from(listEl.querySelectorAll('.instance-task-row'));
	return rows.slice(0, InputValidation.limits.tasksPerInstance).map(row => {
		const nameInput = row.querySelector('.instance-task-name');
		const enabledInput = row.querySelector('.instance-task-enabled');
		const exprInput = row.querySelector('.instance-task-expr');
		const actionSelect = row.querySelector('.instance-task-action');
		const commandInput = row.querySelector('.instance-task-command');
		const useKillStopInput = row.querySelector('.instance-task-use-kill-stop-input');
		const strictRestartInput = row.querySelector('.instance-task-strict-restart-input');

		const name = InputValidation.truncateText(nameInput.value || '', InputValidation.limits.taskName).trim();
		const enabled = enabledInput.checked !== false;
		const expr = InputValidation.truncateText(exprInput.value || '', InputValidation.limits.taskExpr).trim();
		const action = (actionSelect.value || '').trim();
		const command = InputValidation.truncateText(commandInput.value || '', InputValidation.limits.taskCommand).trim();
		const canUseKillStop = action === 'stop' || action === 'restart';
		const isRestart = action === 'restart';
		const useKillStop = canUseKillStop && useKillStopInput.checked === true;
		const strictRestart = isRestart && strictRestartInput.checked === true;
		return { name, enabled, expr, action, command, use_kill_stop: useKillStop, strict_restart: strictRestart };
	});
};

export const fillInstanceModalForm = ({
	form,
	ins = {},
	nameInput,
	groupSelect,
	groupInput,
	groups,
	setInstanceEditorValue,
	stopCommandInput,
	cleanupCommandInput,
	autostartInput,
	autoRestartInput,
	terminalSelect,
	inputEncodingSelect,
	outputEncodingSelect,
	startPriorityInput,
	restartIntervalInput,
	taskList,
	accessLinksInput,
}) => {
	form?.reset?.();
	if (nameInput) {
		nameInput.value = ins.name || '';
	}
	renderGroupSelectOptions({ select: groupSelect, selectedGroup: ins.group || '', groups });
	if (groupSelect && groupSelect.value === NEW_GROUP_VALUE) {
		toggleNewGroupInput(groupInput, true);
		if (groupInput) {
			groupInput.value = String(ins.group || '').trim();
		}
	} else {
		toggleNewGroupInput(groupInput, false);
	}
	setInstanceEditorValue('path', ins.path || './instances/');
	setInstanceEditorValue('command', ins.command || '');
	if (stopCommandInput) {
		stopCommandInput.value = ins.stop_command || '';
	}
	if (cleanupCommandInput) {
		cleanupCommandInput.value = ins.cleanup_command || '';
	}
	if (autostartInput) {
		autostartInput.checked = !!ins.auto_start;
	}
	if (autoRestartInput) {
		autoRestartInput.checked = !!ins.auto_restart;
	}
	if (terminalSelect) {
		terminalSelect.value = String(normalizeTerminalMode(ins.terminal ?? terminalMode.TERMINAL));
	}
	renderEncodingOptions(inputEncodingSelect, ins.input_encoding || DEFAULT_TERMINAL_ENCODING);
	renderEncodingOptions(outputEncodingSelect, ins.output_encoding || DEFAULT_TERMINAL_ENCODING);
	if (startPriorityInput) {
		startPriorityInput.value = Number.isInteger(ins.start_priority) ? String(ins.start_priority) : '';
	}
	if (restartIntervalInput) {
		restartIntervalInput.value = Number.isInteger(ins.restart_interval) ? String(ins.restart_interval) : '';
	}
	if (accessLinksInput) {
		accessLinksInput.value = InputValidation.instance.formatAccessLinksText(ins.access_links);
	}
	renderInstanceTasks(taskList, Array.isArray(ins.tasks) ? ins.tasks : []);
};
