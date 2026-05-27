import { mainModalOverlay, state } from "../ui.js";
import { clearTimer, closeAnimatedModal, normalizeSingleLineText, openAnimatedModal, withActionsDisabled } from '../utils/utils.js';
import { createInstance, deleteInstance, fetchInstance, updateInstance } from '../api/instance.js';
import { bootTerminalWorkspace } from '../module/terminal.js';
import { bootFileEditorModal } from '../module/fileEditorModal.js';
import { bootFileManager, cleanupFileLastDirs, ensureFileManagerDom, getUpdateDirDisplayName, renameFileLastDirsInstance } from '../module/fileList.js';
import { showAlert, showConfirm } from '../module/dialog.js';
import { InputValidation } from '../utils/inputValidation.js';
import { normalizeTerminalMode, terminalMode } from '../utils/enum.js';
import { setupAutoResizeTextarea } from '../utils/autoTextarea.js';
import {
	buildTaskRow,
	collectInstanceTasks,
	fillInstanceModalForm,
	getExistingGroups,
	getUniqueTaskName,
	NEW_GROUP_VALUE,
	normalizeEncodingValue,
	renderGroupSelectOptions,
	renderInstanceTasks,
	toggleNewGroupInput,
} from '../module/instanceModalShared.js';
import {
	applyInstanceModalPageState,
	bindInstanceModalViewEvents,
	closeInstanceHelpModal,
	openInstanceHelpModal,
} from '../module/instanceModalView.js';

console.log('[页面] 控制台页加载中...');

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="instanceModal" class="modal-overlay">
		<div class="modal-card instance-modal-card">
			<div class="modal-header">
				<span id="instanceModalTitle" class="modal-title">CREATE INSTANCE</span>
				<button id="instanceModalClose" class="modal-close" type="button">×</button>
			</div>
			<form id="instanceModalForm" class="modal-form" novalidate>
				<div class="filter-group instance-modal-tabs">
					<button id="instanceModalTabBasic" class="filter-btn active" type="button" data-page="basic">BASIC</button>
					<button id="instanceModalTabAdvanced" class="filter-btn" type="button" data-page="advanced">ADVANCED</button>
					<button id="instanceModalTabTasks" class="filter-btn" type="button" data-page="tasks">TASKS</button>
					<button id="instanceModalTabDelete" class="filter-btn page-switcher-tab-push-end" type="button" data-page="delete">DELETE</button>
				</div>
				<div class="instance-modal-body modal-card-loading-content">
					<div id="instanceModalPageBasic" class="instance-modal-page active">
						<div class="field-group">
							<span>NAME</span>
							<input id="instanceModalName" name="name" type="text" maxlength="32" autocomplete="off" required>
						</div>
						<div class="field-group">
							<span>PATH</span>
							<textarea id="instanceModalPath" name="path" class="input auto-textarea auto-textarea-single-line" rows="1" maxlength="4096" spellcheck="false" placeholder=" ./instances/"></textarea>
						</div>
						<div class="field-group">
							<span>START COMMAND</span>
							<textarea id="instanceModalCommand" name="command" class="input auto-textarea auto-textarea-single-line" rows="1" maxlength="4096" spellcheck="false"></textarea>
						</div>
						<div class="inline-config-row">
							<label class="checkbox-group">
								<input id="instanceModalAutostart" name="auto_start" type="checkbox">
								<span>AUTO START</span>
							</label>
							<div class="field-group inline-field-group">
								<input id="instanceModalStartPriority" name="start_priority" type="number" min="-99999999" max="99999999" step="1" inputmode="numeric" placeholder=" Priority | 1 > 0 > -1">
							</div>
						</div>
						<div class="inline-config-row">
							<label class="checkbox-group">
								<input id="instanceModalAutoRestart" name="auto_restart" type="checkbox">
								<span>AUTO RESTART</span>
							</label>
							<div class="field-group inline-field-group">
								<input id="instanceModalRestartInterval" name="restart_interval" type="number" min="0" max="86400000" step="1" inputmode="numeric" placeholder=" Interval | auto_restart_interval | ms">
							</div>
						</div>
						<div class="instance-group-row">
							<span>GROUP</span>
							<div class="select-wrapper">
								<select id="instanceModalGroupSelect" name="group_select" autocomplete="off"></select>
							</div>
							<span class="placeholder"></span>
							<input id="instanceModalGroupNew" class="hidden" name="group" type="text" maxlength="32" autocomplete="off" placeholder=" Group name">
						</div>
					</div>
					<div id="instanceModalPageAdvanced" class="instance-modal-page">
						<div class="field-group field-group-dynamic-label">
							<span>TERMINAL</span>
							<div class="select-wrapper">
								<select id="instanceModalTerminal" name="terminal" autocomplete="off">
									<option value="${terminalMode.PTY_TERMINAL}">PTY_TERMINAL [仿真终端]</option>
									<option value="${terminalMode.TERMINAL}">TERMINAL [普通终端]</option>
									<option value="${terminalMode.NO_TERMINAL}">NO_TERMINAL [无终端]</option>
								</select>
							</div>
							<div class="file-action-static instance-advanced-note">
								重启实例以生效
							</div>
						</div>
						<div class="field-group instance-stop-command-field"><span>STOP COMMAND</span><input id="instanceModalStopCommand" name="stop_command" type="text" autocomplete="off" maxlength="4096" placeholder=" ^C / stop"></div>
						<div class="field-group instance-cleanup-command-field"><span>CLEANUP COMMAND</span><input id="instanceModalCleanupCommand" name="cleanup_command" type="text" autocomplete="off" maxlength="4096" placeholder=" cleanup after stop"></div>
						<div class="instance-encoding-row">
							<div class="field-group instance-encoding-field"><span>INPUT ENCODING</span><div class="select-wrapper"><select id="instanceModalInputEncoding" name="input_encoding" autocomplete="off"></select></div></div>
							<div class="field-group instance-encoding-field"><span>OUTPUT ENCODING</span><div class="select-wrapper"><select id="instanceModalOutputEncoding" name="output_encoding" autocomplete="off"></select></div></div>
						</div>
						<div class="field-group">
							<span>ACCESS LINKS</span>
							<textarea id="instanceModalAccessLinks" name="access_links" class="instance-access-links-input" rows="4" maxlength="2048" spellcheck="false" placeholder="# Comment&#10;http://127.0.0.1:80&#10;Docs: http://127.0.0.1:80/docs&#10;Link: 127.0.0.1:80"></textarea>
						</div>
					</div>
					<div id="instanceModalPageTasks" class="instance-modal-page">
						<div class="instance-tasks-toolbar">
							<button id="instanceTaskHelpToggle" class="btn instance-task-help-toggle" type="button">HELP</button>
							<button id="instanceTaskAdd" class="btn" type="button">ADD TASK</button>
						</div>
						<div id="instanceTasksList" class="instance-tasks-list"></div>
					</div>
					<div id="instanceModalPageDelete" class="instance-modal-page">
						<div class="field-group"><span>PATH</span><div id="instanceDeletePath" class="file-action-static"></div></div>
						<div class="field-group field-group-dynamic-label">
							<span>DELETE INSTANCE</span>
							<div class="file-action-static file-delete-warning">
								输入实例名称以确认删除:
								<code id="instanceDeleteHintName"></code>
							</div>
						</div>
						<div class="field-group"><span>NAME</span><input id="instanceDeleteName" name="delete_name" type="text" autocomplete="off" maxlength="32" placeholder=" Type instance name"></div>
						<label class="checkbox-group"><input id="instanceDeleteFiles" name="delete_files" type="checkbox"><span>DELETE INSTANCE FILES [同时删除实例文件]</span></label>
					</div>
				</div>
				<div class="modal-actions modal-actions-split">
					<span id="instanceModalStatus" aria-live="polite"></span>
					<div class="modal-actions-group">
						<button class="btn" type="button" id="instanceModalCancel">CANCEL</button>
						<button class="btn btn-start" type="submit" id="instanceModalSubmit">CREATE</button>
					</div>
				</div>
			</form>
		</div>
	</div>
	<div id="instanceTasksHelpModal" class="modal-overlay">
		<div class="modal-card instance-help-modal-card">
			<div class="modal-header">
				<span class="modal-title">TASK HELP</span>
				<button id="instanceTasksHelpClose" class="modal-close" type="button">×</button>
			</div>
			<div class="modal-form">
				<div class="instance-tasks-help-content">
					<table>
						<thead>
							<tr>
								<th align="left">规则</th>
								<th align="left">说明</th>
							</tr>
						</thead>
						<tbody>
							<tr><td>语法标准</td><td>计划任务使用 <strong>Quartz Cron</strong> 语法, 5 段会自动补全为 6 段</td></tr>
							<tr><td>字段分隔</td><td>各段之间使用空格分隔, 一共支持 5 / 6 / 7 段</td></tr>
							<tr><td>日与周</td><td><strong>日期</strong> 与 <strong>星期</strong> 通常二选一明确指定, 另一个建议写 <code>?</code></td></tr>
							<tr><td>英文名称</td><td>月份和星期支持英文缩写, 如 <code>JAN</code>、<code>MON-FRI</code>, 且不区分大小写</td></tr>
						</tbody>
					</table>
					<table>
						<thead>
							<tr>
								<th align="left">段数</th>
								<th align="left">表达式格式</th>
								<th align="left">各段含义</th>
							</tr>
						</thead>
						<tbody>
							<tr><td>5</td><td><code>分 时 日 月 周</code></td><td>自动补全为 <code>0 分 时 日 月 周</code></td></tr>
							<tr><td>6</td><td><code>秒 分 时 日 月 周</code></td><td>标准 6 段格式 (Quartz)</td></tr>
							<tr><td>7</td><td><code>秒 分 时 日 月 周 年</code></td><td>增加年份限制</td></tr>
						</tbody>
					</table>
					<table>
						<thead>
							<tr>
								<th align="left">位置</th>
								<th align="left">字段名称</th>
								<th align="left">取值范围</th>
								<th align="left">特殊字符</th>
							</tr>
						</thead>
						<tbody>
							<tr><td>1</td><td>秒 (Seconds)</td><td>0 - 59</td><td><code>, - * /</code></td></tr>
							<tr><td>2</td><td>分 (Minutes)</td><td>0 - 59</td><td><code>, - * /</code></td></tr>
							<tr><td>3</td><td>时 (Hours)</td><td>0 - 23</td><td><code>, - * /</code></td></tr>
							<tr><td>4</td><td>日 (Day of Month)</td><td>1 - 31</td><td><code>, - * / ? L W</code></td></tr>
							<tr><td>5</td><td>月 (Month)</td><td>1 - 12</td><td><code>, - * /</code></td></tr>
							<tr><td>6</td><td>周 (Day of Week)</td><td>1 - 7</td><td><code>, - * / ? L #</code></td></tr>
							<tr><td>7</td><td>年 (Year)</td><td>1970+</td><td><code>, - * /</code></td></tr>
						</tbody>
					</table>
					<table>
						<thead>
							<tr>
								<th align="left">特殊字符</th>
								<th align="left">字段名称</th>
								<th align="left">描述</th>
							</tr>
						</thead>
						<tbody>
							<tr><td><code>*</code></td><td>任意值</td><td>该字段每次都匹配</td></tr>
							<tr><td><code>?</code></td><td>留空</td><td>仅用于"日"和"周", 表示这一项不指定</td></tr>
							<tr><td><code>-</code></td><td>范围</td><td>表示一个连续区间</td></tr>
							<tr><td><code>,</code></td><td>多个值</td><td>表示多个离散取值</td></tr>
							<tr><td><code>/</code></td><td>步长</td><td>表示按固定间隔触发</td></tr>
							<tr><td><code>L</code></td><td>最后</td><td>表示最后一天或最后一个星期几</td></tr>
							<tr><td><code>W</code></td><td>最近工作日</td><td>仅用于"日", 表示最近的工作日</td></tr>
							<tr><td><code>#</code></td><td>第几个</td><td>仅用于"周", 表示第几个星期几</td></tr>
						</tbody>
					</table>
					<table>
						<thead>
							<tr>
								<th align="left">示例</th>
								<th align="left">含义</th>
							</tr>
						</thead>
						<tbody>
							<tr><td><code>0 * * * * ?</code></td><td>每分钟执行一次</td></tr>
							<tr><td><code>0 */5 * * * ?</code></td><td>每 5 分钟执行一次</td></tr>
							<tr><td><code>0 0 3 * * ?</code></td><td>每天 03:00 执行</td></tr>
							<tr><td><code>0 30 3 * * ?</code></td><td>每天 03:30 执行</td></tr>
							<tr><td><code>0 0 */6 * * ?</code></td><td>每 6 小时执行一次</td></tr>
							<tr><td><code>0 0 9 ? * MON-FRI</code></td><td>工作日每天 09:00 执行</td></tr>
							<tr><td><code>0 0 9,18 ? * MON-FRI</code></td><td>工作日每天 09:00 和 18:00 执行</td></tr>
							<tr><td><code>0 0 4 ? * MON</code></td><td>每周一 04:00 执行</td></tr>
							<tr><td><code>0 0 4 ? * SAT,SUN</code></td><td>每周六和周日 04:00 执行</td></tr>
							<tr><td><code>0 */15 * * * ?</code></td><td>每 15 分钟执行一次</td></tr>
							<tr><td><code>0 0 4 1 * ?</code></td><td>每月 1 日 04:00 执行</td></tr>
							<tr><td><code>0 0 4 1 */3 ?</code></td><td>每 3 个月的 1 日 04:00 执行</td></tr>
							<tr><td><code>0 0 10 L * ?</code></td><td>每月最后一天 10:00 执行</td></tr>
							<tr><td><code>0 0 10 LW * ?</code></td><td>每月最后一个工作日 10:00 执行</td></tr>
							<tr><td><code>0 0 8 ? * 2#1</code></td><td>每月第 1 个星期一 08:00 执行</td></tr>
							<tr><td><code>0 0 8 15W * ?</code></td><td>每月 15 日最近的工作日 08:00 执行</td></tr>
							<tr><td><code>0 0 0 1 1 ?</code></td><td>每年 1 月 1 日 00:00 执行</td></tr>
							<tr><td><code>0 30 9 * * ? 2027</code></td><td>仅在 2027 年每天 09:30 执行</td></tr>
						</tbody>
					</table>
				</div>
				<div class="modal-actions"><button id="instanceTasksHelpOk" class="btn btn-start" type="button">OK</button></div>
			</div>
		</div>
	</div>
`);

const dom = {
	section: document.getElementById('terminalSection'),
	navHome: document.getElementById('navHome'),
	navSep: document.getElementById('navSep'),
	navCurrent: document.getElementById('navCurrent'),
	navFileSep: document.getElementById('navFileSep'),
	navFiles: document.getElementById('navFiles'),
	// file list / file modals are managed by module/fileManager.js
	// file editor modal is managed by module/textEditor.js
	instanceModal: document.getElementById('instanceModal'),
	instanceModalCard: document.querySelector('#instanceModal .instance-modal-card'),
	instanceTasksHelpModal: document.getElementById('instanceTasksHelpModal'),
	instanceTasksHelpClose: document.getElementById('instanceTasksHelpClose'),
	instanceTasksHelpOk: document.getElementById('instanceTasksHelpOk'),
	instanceModalForm: document.getElementById('instanceModalForm'),
	instanceModalClose: document.getElementById('instanceModalClose'),
	instanceModalCancel: document.getElementById('instanceModalCancel'),
	instanceModalTitle: document.getElementById('instanceModalTitle'),
	instanceModalStatus: document.getElementById('instanceModalStatus'),
	instanceModalSubmit: document.getElementById('instanceModalSubmit'),
	instanceModalActions: document.querySelector('#instanceModalForm .modal-actions'),
	instanceModalName: document.getElementById('instanceModalName'),
	instanceModalGroupSelect: document.getElementById('instanceModalGroupSelect'),
	instanceModalGroupNew: document.getElementById('instanceModalGroupNew'),
	instanceModalPath: document.getElementById('instanceModalPath'),
	instanceModalCommand: document.getElementById('instanceModalCommand'),
	instanceModalStopCommand: document.getElementById('instanceModalStopCommand'),
	instanceModalCleanupCommand: document.getElementById('instanceModalCleanupCommand'),
	instanceModalAutostart: document.getElementById('instanceModalAutostart'),
	instanceModalStartPriority: document.getElementById('instanceModalStartPriority'),
	instanceModalAutoRestart: document.getElementById('instanceModalAutoRestart'),
	instanceModalRestartInterval: document.getElementById('instanceModalRestartInterval'),
	instanceModalTerminal: document.getElementById('instanceModalTerminal'),
	instanceModalInputEncoding: document.getElementById('instanceModalInputEncoding'),
	instanceModalOutputEncoding: document.getElementById('instanceModalOutputEncoding'),
	instanceModalAccessLinks: document.getElementById('instanceModalAccessLinks'),
	instanceModalTabs: document.querySelectorAll('#instanceModalForm .instance-modal-tabs .filter-btn'),
	instanceModalTabDelete: document.getElementById('instanceModalTabDelete'),
	instanceModalPageBasic: document.getElementById('instanceModalPageBasic'),
	instanceModalPageAdvanced: document.getElementById('instanceModalPageAdvanced'),
	instanceModalPageTasks: document.getElementById('instanceModalPageTasks'),
	instanceModalPageDelete: document.getElementById('instanceModalPageDelete'),
	instanceDeleteFiles: document.getElementById('instanceDeleteFiles'),
	instanceDeletePath: document.getElementById('instanceDeletePath'),
	instanceTasksList: document.getElementById('instanceTasksList'),
	instanceTaskAdd: document.getElementById('instanceTaskAdd'),
	instanceTaskHelpToggle: document.getElementById('instanceTaskHelpToggle'),
	instanceDeleteName: document.getElementById('instanceDeleteName'),
	instanceDeleteHintName: document.getElementById('instanceDeleteHintName'),
};

const pageState = {
    instanceModalMode: 'create',
    editingInstanceName: null,
	modalCloseTimer: null,
	instanceModalPage: 'basic',
	currentTerminalSvc: null,
	currentInstanceUpdateStagingDirName: '',
	instanceModalLoadSeq: 0,
	instanceModalLoading: false,
};

const createFileSelectionController = () => {
	let selection = { include: [], exclude: [] };
	let count = 0;
	let allSelected = false;
	let selectionVersion = 0;
	const listeners = new Set();

	const cloneRules = (rules) => Array.isArray(rules)
		? rules.map((rule) => ({
			path: String(rule?.path || ''),
			is_dir: !!(rule?.is_dir ?? rule?.isDir),
		}))
		: [];

	const emit = () => {
		const snapshot = {
			selection,
			count,
			allSelected,
			selectionVersion,
		};
		listeners.forEach((listener) => listener(snapshot));
	};

	const getSelection = () => selection;
	const getSnapshot = () => ({ selection, count, allSelected, selectionVersion });
	const replaceSelection = (nextSelection) => {
		const next = {
			include: cloneRules(nextSelection?.include),
			exclude: cloneRules(nextSelection?.exclude),
		};
		const prevJson = JSON.stringify(selection);
		const nextJson = JSON.stringify(next);
		selection = next;
		if (prevJson === nextJson) {
			emit();
			return;
		}
		selectionVersion += 1;
		emit();
	};

	const setSelection = (nextSelection) => {
		replaceSelection(nextSelection);
	};

	const updateSelection = (updater) => {
		if (typeof updater !== 'function') {
			return;
		}
		const draft = {
			include: cloneRules(selection.include),
			exclude: cloneRules(selection.exclude),
		};
		const nextSelection = updater(draft) || draft;
		replaceSelection(nextSelection);
	};

	const clearSelection = () => {
		setSelection({ include: [], exclude: [] });
	};

	const setUiState = (nextState = {}) => {
		const nextCount = Math.max(0, Number(nextState.count || 0));
		const nextAllSelected = nextState.allSelected === true;
		if (nextCount === count && nextAllSelected === allSelected) {
			return;
		}
		count = nextCount;
		allSelected = nextAllSelected;
		emit();
	};

	const subscribe = (listener) => {
		if (typeof listener !== 'function') {
			return () => {};
		}
		listeners.add(listener);
		listener(getSnapshot());
		return () => listeners.delete(listener);
	};

	return {
		getSelection,
		getSnapshot,
		setSelection,
		updateSelection,
		clearSelection,
		setUiState,
		subscribe,
	};
};

let controller = null;
let terminalCard = null;
let fileEditorModal = null;
let fileManager = null;
let fileSelection = null;

const getRuntimeInstanceUpdateStagingDirName = (settings = {}) => {
	const updateDir = String(settings.instanceUpdateStagingDir || '').trim();
	if (!updateDir) {
		return pageState.currentInstanceUpdateStagingDirName;
	}
	return getUpdateDirDisplayName(updateDir);
};

const applyRuntimeSettingsToTerminalPage = (settings = {}) => {
	if (!settings || typeof settings !== 'object') {
		return;
	}
	if (Number.isFinite(settings.historySize)) {
		state.historySize = settings.historySize;
		terminalCard?.applyHistorySize?.(settings.historySize);
	}
	const updateDirName = getRuntimeInstanceUpdateStagingDirName(settings);
	if (pageState.currentInstanceUpdateStagingDirName !== updateDirName) {
		pageState.currentInstanceUpdateStagingDirName = updateDirName;
		if (state.currentInstanceName) {
			void fileManager?.loadFiles?.(undefined, undefined, {
				instanceName: state.currentInstanceName,
				instanceUpdateStagingDirName: pageState.currentInstanceUpdateStagingDirName,
			});
		}
	}
};

const bindSingleLineEditor = (el, maxLength = 0) => {
    if (!el) return;
	const normalizeEditorValue = () => InputValidation.truncateText(normalizeSingleLineText(el.value || ''), maxLength || Number.MAX_SAFE_INTEGER);
	const scheduleResize = setupAutoResizeTextarea(el);

    el.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
            event.preventDefault();
        }
    });

	    el.addEventListener('paste', (event) => {
		event.preventDefault();
		const text = normalizeSingleLineText(event.clipboardData?.getData('text/plain') || '');
		const selectionStart = el.selectionStart;
		const selectionEnd = el.selectionEnd;
		const nextValue = `${el.value.slice(0, selectionStart)}${text}${el.value.slice(selectionEnd)}`;
		const normalizedValue = InputValidation.truncateText(normalizeSingleLineText(nextValue), maxLength || Number.MAX_SAFE_INTEGER);
		const nextCursor = Math.min(normalizeSingleLineText(`${el.value.slice(0, selectionStart)}${text}`).length, normalizedValue.length);
		el.value = normalizedValue;
		el.setSelectionRange(nextCursor, nextCursor);
		scheduleResize();
    });

	el.addEventListener('blur', () => {
		el.value = normalizeEditorValue();
		scheduleResize();
	});
};

bindSingleLineEditor(dom.instanceModalCommand, InputValidation.limits.instanceCommand);
bindSingleLineEditor(dom.instanceModalPath, InputValidation.limits.instancePath);

const openInstanceModal = () => {
	openAnimatedModal(dom.instanceModal);
	requestAnimationFrame(() => {
		setupAutoResizeTextarea(dom.instanceModalPath)();
		setupAutoResizeTextarea(dom.instanceModalCommand)();
	});
};

const focusInstanceNameInput = () => {
	requestAnimationFrame(() => {
		dom.instanceModalName.focus();
	});
};

const setInstanceModalStatus = (text, error = false) => {
	if (!dom.instanceModalStatus) return;
	dom.instanceModalStatus.textContent = String(text || '');
	dom.instanceModalStatus.classList.toggle('error', !!error);
};

const setInstanceModalLoading = (loading) => {
	pageState.instanceModalLoading = !!loading;
	if (dom.instanceModalCard) dom.instanceModalCard.classList.toggle('modal-card-loading', !!loading);
	if (dom.instanceModalSubmit) dom.instanceModalSubmit.disabled = !!loading;
};

const clearInstanceModalForLoad = () => {
	dom.instanceModalForm.reset();
	if (dom.instanceModalName) dom.instanceModalName.value = '';
	if (dom.instanceModalGroupSelect) dom.instanceModalGroupSelect.replaceChildren();
	if (dom.instanceModalGroupNew) {
		dom.instanceModalGroupNew.value = '';
		dom.instanceModalGroupNew.classList.add('hidden');
	}
	setInstanceEditorValue('path', '');
	setInstanceEditorValue('command', '');
	if (dom.instanceModalStopCommand) dom.instanceModalStopCommand.value = '';
	if (dom.instanceModalCleanupCommand) dom.instanceModalCleanupCommand.value = '';
	if (dom.instanceModalAutostart) dom.instanceModalAutostart.checked = false;
	if (dom.instanceModalStartPriority) dom.instanceModalStartPriority.value = '';
	if (dom.instanceModalAutoRestart) dom.instanceModalAutoRestart.checked = false;
	if (dom.instanceModalRestartInterval) dom.instanceModalRestartInterval.value = '';
	if (dom.instanceModalTerminal) dom.instanceModalTerminal.value = String(terminalMode.TERMINAL);
	if (dom.instanceModalInputEncoding) dom.instanceModalInputEncoding.replaceChildren();
	if (dom.instanceModalOutputEncoding) dom.instanceModalOutputEncoding.replaceChildren();
	if (dom.instanceModalAccessLinks) dom.instanceModalAccessLinks.value = '';
	renderInstanceTasksInModal([]);
	if (dom.instanceDeleteName) dom.instanceDeleteName.value = '';
	if (dom.instanceDeleteFiles) dom.instanceDeleteFiles.checked = false;
	if (dom.instanceDeletePath) dom.instanceDeletePath.textContent = '';
	if (dom.instanceDeleteHintName) dom.instanceDeleteHintName.textContent = '';
	setInstanceModalStatus('LOADING...');
};

const bindGroupSelectEvents = () => {
	if (!dom.instanceModalGroupSelect) {
		return;
	}
	dom.instanceModalGroupSelect.onchange = () => {
		const v = dom.instanceModalGroupSelect.value;
		toggleNewGroupInput(dom.instanceModalGroupNew, v === NEW_GROUP_VALUE);
		if (v === NEW_GROUP_VALUE) {
			dom.instanceModalGroupNew.focus();
		}
	};

	if (dom.instanceModalGroupNew) {
		dom.instanceModalGroupNew.oninput = () => {
			if (!dom.instanceModalGroupSelect) return;
			if (dom.instanceModalGroupSelect.value !== NEW_GROUP_VALUE) return;
			renderGroupSelectOptions({
				select: dom.instanceModalGroupSelect,
				selectedGroup: dom.instanceModalGroupNew.value,
				groups: getExistingGroups(state.instances || []),
			});
			dom.instanceModalGroupSelect.value = NEW_GROUP_VALUE;
		};
	}
};

// NOTE: File editor logic moved to module/textEditor.js


const fillInstanceModal = (ins = {}) => {
	fillInstanceModalForm({
		form: dom.instanceModalForm,
		ins,
		nameInput: dom.instanceModalName,
		groupSelect: dom.instanceModalGroupSelect,
		groupInput: dom.instanceModalGroupNew,
		groups: getExistingGroups(state.instances || []),
		setInstanceEditorValue,
		stopCommandInput: dom.instanceModalStopCommand,
		cleanupCommandInput: dom.instanceModalCleanupCommand,
		autostartInput: dom.instanceModalAutostart,
		autoRestartInput: dom.instanceModalAutoRestart,
		terminalSelect: dom.instanceModalTerminal,
		inputEncodingSelect: dom.instanceModalInputEncoding,
		outputEncodingSelect: dom.instanceModalOutputEncoding,
		startPriorityInput: dom.instanceModalStartPriority,
		restartIntervalInput: dom.instanceModalRestartInterval,
		taskList: dom.instanceTasksList,
		accessLinksInput: dom.instanceModalAccessLinks,
	});
	switchInstanceModalPage('basic');
};

const buildCreateInstanceDraft = (options = {}) => {
	const group = String(options.group || '').trim();
	if (!group) {
		return { terminal: terminalMode.TERMINAL };
	}
	return { group, terminal: terminalMode.TERMINAL };
};

const switchInstanceModalPage = (page) => {
	applyInstanceModalPageState({
		dom,
		pageState,
		modalState: getInstanceModalState(),
		page,
	});
	if (page === 'basic') {
		requestAnimationFrame(() => {
			setupAutoResizeTextarea(dom.instanceModalPath)();
			setupAutoResizeTextarea(dom.instanceModalCommand)();
		});
	}
};

const bindInstanceModalTabs = () => {
	bindInstanceModalViewEvents({
		dom,
		onSwitchPage: switchInstanceModalPage,
		onOpenHelp: openInstanceTasksHelpModal,
	});
};

const renderInstanceTasksInModal = (tasks) => renderInstanceTasks(dom.instanceTasksList, tasks);
const collectInstanceTasksInModal = () => collectInstanceTasks(dom.instanceTasksList);
const getUniqueTaskNameInModal = () => getUniqueTaskName(dom.instanceTasksList);

export const getInstanceEditorValue = (type) => {
	if (type === 'path') {
		return dom.instanceModalPath ? normalizeSingleLineText(dom.instanceModalPath.value || '') : './instances/';
	}
    if (type === 'command') {
        return dom.instanceModalCommand ? normalizeSingleLineText(dom.instanceModalCommand.value || '') : '';
    }
    return '';
};

export const setInstanceEditorValue = (type, value) => {
    if (type === 'path' && dom.instanceModalPath) {
		dom.instanceModalPath.value = normalizeSingleLineText(value || '');
		setupAutoResizeTextarea(dom.instanceModalPath)();
    }
    if (type === 'command' && dom.instanceModalCommand) {
		dom.instanceModalCommand.value = normalizeSingleLineText(value || '');
		setupAutoResizeTextarea(dom.instanceModalCommand)();
    }
};

export const openTerminalPage = async (svc, historySize, options = {}) => {
	if (!svc?.name) {
		throw new Error('缺少实例名称');
	}
	const sessionId = Number(options.sessionId) || state.instanceSessionSeq;
	const detailResult = await fetchInstance(svc.name);
	if (!detailResult.ok || !detailResult.data) {
		throw new Error(detailResult.error || `加载实例失败: ${svc.name}`);
	}
	const detail = detailResult.data;
	if (sessionId !== state.instanceSessionSeq) {
		return false;
	}
	Object.assign(detail, {
		running: !!svc.running,
		updating: !!svc.updating,
		restarting: !!svc.restarting,
		start_time: svc.start_time || detail.start_time || '',
		restart_count: Number.isFinite(svc.restart_count) ? svc.restart_count : (detail.restart_count || 0),
		terminal: normalizeTerminalMode(detail.terminal),
		active_terminal: normalizeTerminalMode(svc.active_terminal ?? detail.active_terminal ?? detail.terminal),
	});
	const terminalSvc = { ...detail };
    state.currentInstanceName = detail.name;
	pageState.currentTerminalSvc = terminalSvc;
	pageState.currentInstanceUpdateStagingDirName = getUpdateDirDisplayName(terminalSvc.instance_update_staging_dir);
	state.historySize = Number.isFinite(detail.history_size) ? detail.history_size : historySize;
	// terminal DOM 是动态插入的，确保 file manager 读到最新节点。
	ensureFileManagerDom();
    fileManager?.reset();

	dom.navCurrent.innerText = '终端';
	terminalCard?.open({ svc: terminalSvc, historySize });
	return true;
};

export const closeTerminalPage = () => {
	fileManager?.close();
	fileEditorModal?.close();
	terminalCard?.close();
	state.instanceSessionSeq++;
    state.currentInstanceName = null;
    state.currentInstanceObj = null;
	pageState.currentTerminalSvc = null;
	pageState.currentInstanceUpdateStagingDirName = '';
	fileManager?.reset();
};

export const prepareOpenTerminalPage = () => {
	terminalCard?.clearView?.();
	fileManager?.reset?.();
};

export const patchCurrentTerminalInstance = (patch = {}) => {
	if (!pageState.currentTerminalSvc) {
		return;
	}
	pageState.currentTerminalSvc = {
		...pageState.currentTerminalSvc,
		...patch,
	};
	terminalCard?.setCurrentSvc?.(pageState.currentTerminalSvc);
	if (Object.prototype.hasOwnProperty.call(patch, 'instance_update_staging_dir')) {
		pageState.currentInstanceUpdateStagingDirName = getUpdateDirDisplayName(pageState.currentTerminalSvc.instance_update_staging_dir);
	}
};

export const openCreateInstanceModal = (options = {}) => {
    pageState.instanceModalMode = 'create';
    pageState.editingInstanceName = null;
    pageState.modalCloseTimer = clearTimer(pageState.modalCloseTimer);
	pageState.instanceModalLoadSeq += 1;
	setInstanceModalLoading(false);
	setInstanceModalStatus('');
	fillInstanceModal(buildCreateInstanceDraft(options));
    if (dom.instanceModalTitle) {
        dom.instanceModalTitle.innerText = 'CREATE INSTANCE';
    }
    if (dom.instanceModalSubmit) {
        dom.instanceModalSubmit.innerText = 'CREATE';
    }
	openInstanceModal();
	if (dom.instanceModalTabDelete) {
		dom.instanceModalTabDelete.style.display = 'none';
	}
	switchInstanceModalPage('basic');
	focusInstanceNameInput();
};

export const openEditInstanceModal = async (svc) => {
	if (!svc?.name) return;
	const loadSeq = pageState.instanceModalLoadSeq + 1;
	pageState.instanceModalLoadSeq = loadSeq;
	pageState.instanceModalMode = 'edit';
	pageState.editingInstanceName = String(svc.name || '').trim();
	pageState.modalCloseTimer = clearTimer(pageState.modalCloseTimer);
	clearInstanceModalForLoad();
	setInstanceModalLoading(true);
	if (dom.instanceModalTitle) {
		dom.instanceModalTitle.innerText = 'EDIT INSTANCE';
	}
	if (dom.instanceModalSubmit) {
		dom.instanceModalSubmit.innerText = 'SAVE';
	}
	openInstanceModal();
	if (dom.instanceModalTabDelete) {
		dom.instanceModalTabDelete.style.display = '';
	}
	switchInstanceModalPage('basic');
	const detailResult = await fetchInstance(svc.name);
	if (pageState.instanceModalLoadSeq !== loadSeq) return;
	if (!detailResult.ok || !detailResult.data) {
		setInstanceModalStatus(detailResult.error || '加载实例配置失败', true);
		setInstanceModalLoading(true);
		return;
	}
	const detail = detailResult.data;

    pageState.instanceModalMode = 'edit';
    pageState.editingInstanceName = detail.name;
    fillInstanceModal(detail);
    if (dom.instanceModalTitle) {
        dom.instanceModalTitle.innerText = 'EDIT INSTANCE';
    }
    if (dom.instanceModalSubmit) {
        dom.instanceModalSubmit.innerText = 'SAVE';
    }
	openInstanceModal();
	if (dom.instanceModalTabDelete) {
		dom.instanceModalTabDelete.style.display = '';
	}
	if (dom.instanceDeleteName) {
		dom.instanceDeleteName.value = '';
	}
	if (dom.instanceDeleteFiles) {
		dom.instanceDeleteFiles.checked = false;
	}
	if (dom.instanceDeletePath) {
		dom.instanceDeletePath.textContent = String(detail.path || './instances/').trim() || './instances/';
	}
	setInstanceModalStatus('');
	setInstanceModalLoading(false);
	switchInstanceModalPage('basic');
	focusInstanceNameInput();
};

export const closeInstanceModal = () => {
	closeInstanceTasksHelpModal();
	pageState.modalCloseTimer = closeAnimatedModal(dom.instanceModal, pageState.modalCloseTimer, () => {
		pageState.modalCloseTimer = null;
	});
};

export const getInstanceModalState = () => ({
    mode: pageState.instanceModalMode,
    editingInstanceName: pageState.editingInstanceName,
});

const normalizeInstancePathForCompare = (path) => {
	let normalized = String(path || './instances/').trim() || './instances/';
	normalized = normalized.replace(/\\+/g, '/');
	while (normalized.length > 1 && normalized.endsWith('/')) {
		normalized = normalized.slice(0, -1);
	}
	return normalized;
};

const openInstanceTasksHelpModal = () => {
	openInstanceHelpModal(dom.instanceTasksHelpModal);
};

const closeInstanceTasksHelpModal = () => {
	pageState.modalCloseTimer = closeInstanceHelpModal(dom.instanceTasksHelpModal, pageState.modalCloseTimer, () => {
		pageState.modalCloseTimer = null;
	});
};

const handleInstanceModalSubmit = async (event) => {
    event.preventDefault();
	if (pageState.instanceModalLoading) return;
	await withActionsDisabled(dom.instanceModalActions, async () => {
    if (!controller) {
        return;
    }

	const modalState = getInstanceModalState();
	if (pageState.instanceModalPage === 'delete') {
		if (modalState.mode !== 'edit') {
			await showAlert('删除仅在编辑模式下可用', { title: 'NOTICE' });
			return;
		}
		const editingName = modalState.editingInstanceName;
		if (!editingName) {
			await showAlert('没有可删除的实例', { title: 'NOTICE' });
			return;
		}
		const typed = String(dom.instanceDeleteName.value || '').trim();
		if (typed !== editingName) {
			await showAlert('实例名称不匹配', { title: 'NOTICE' });
			dom.instanceDeleteName.focus();
			return;
		}
		const deleteResult = await deleteInstance(editingName, {
			deleteFiles: dom.instanceDeleteFiles.checked === true,
		});
		if (!deleteResult.ok && deleteResult.confirmRequired) {
			const ok = await showConfirm('还有其他未运行实例也在使用这个目录, 是否继续删除?', {
				title: 'CONFIRM',
				tone: 'warning',
				okText: 'DELETE'
			});
			if (!ok) {
				return;
			}
			const confirmedDeleteResult = await deleteInstance(editingName, {
				deleteFiles: dom.instanceDeleteFiles.checked === true,
				confirmSharedDelete: true,
			});
			if (!confirmedDeleteResult.ok) {
				if (confirmedDeleteResult.unauthorized) {
					return;
				}
				await showAlert(confirmedDeleteResult.error || '删除实例失败', { title: 'ERROR', tone: 'danger' });
				return;
			}
		} else if (!deleteResult.ok) {
			if (deleteResult.unauthorized) {
				return;
			}
			await showAlert(deleteResult.error || '删除实例失败', { title: 'ERROR', tone: 'danger' });
			return;
		}
		closeTerminalPage();
		closeInstanceModal();
		controller.updateUrl(null);
		controller.showInstanceListPage();
		await controller.loadInstances();
		return;
	}

    const name = InputValidation.truncateText(dom.instanceModalName.value, InputValidation.limits.instanceName).trim();
	const path = InputValidation.truncateText(getInstanceEditorValue('path'), InputValidation.limits.instancePath).trim() || './instances/';
	const command = InputValidation.truncateText(getInstanceEditorValue('command'), InputValidation.limits.instanceCommand).trim();
	const stopCommand = dom.instanceModalStopCommand ? InputValidation.truncateText(dom.instanceModalStopCommand.value, InputValidation.limits.instanceStopCommand).trim() : '';
	const cleanupCommand = dom.instanceModalCleanupCommand ? InputValidation.truncateText(dom.instanceModalCleanupCommand.value, InputValidation.limits.instanceCleanupCommand).trim() : '';

	const restartIntervalResult = controller.parseOptionalIntegerInRange(dom.instanceModalRestartInterval, '重启延迟', 0, 86400000);
	if (restartIntervalResult.error) {
		await showAlert(restartIntervalResult.error, { title: 'INPUT' });
		restartIntervalResult.input.focus();
		return;
	}

	const startPriorityResult = controller.parseOptionalIntegerInRange(dom.instanceModalStartPriority, '启动优先级', -99999999, 99999999);
	if (startPriorityResult.error) {
		await showAlert(startPriorityResult.error, { title: 'INPUT' });
		startPriorityResult.input.focus();
		return;
	}
	const payload = {
		name,
		group: '',
		path,
		command,
		access_links: '',
		terminal: normalizeTerminalMode(dom.instanceModalTerminal.value),
		input_encoding: normalizeEncodingValue(dom.instanceModalInputEncoding.value),
		output_encoding: normalizeEncodingValue(dom.instanceModalOutputEncoding.value),
		stop_command: stopCommand,
		cleanup_command: cleanupCommand,
		auto_start: dom.instanceModalAutostart.checked,
		start_priority: startPriorityResult.value,
		auto_restart: dom.instanceModalAutoRestart.checked,
		restart_interval: restartIntervalResult.value,
		tasks: collectInstanceTasksInModal(),
	};
	if (dom.instanceModalGroupSelect) {
		const groupSelect = String(dom.instanceModalGroupSelect.value || '').trim();
		if (groupSelect === NEW_GROUP_VALUE) {
			const newGroup = InputValidation.truncateText(dom.instanceModalGroupNew.value || '', InputValidation.limits.groupName).trim();
			if (!newGroup) {
				await showAlert('分组名称不能为空', { title: 'INPUT' });
				toggleNewGroupInput(dom.instanceModalGroupNew, true);
				dom.instanceModalGroupNew.focus();
				return;
			}
			payload.group = newGroup;
		} else {
			payload.group = groupSelect;
		}
	}
	const fieldError = InputValidation.instance.validateFields({
		name: payload.name,
		group: payload.group,
		path: payload.path,
		command: payload.command,
		terminal: payload.terminal,
		stopCommand: payload.stop_command,
		cleanupCommand: payload.cleanup_command,
	});
	if (fieldError) {
		await showAlert(fieldError, { title: 'INPUT' });
		return;
	}
	const taskError = InputValidation.instance.validateTasks(payload.tasks);
	if (taskError) {
		await showAlert(taskError, { title: 'INPUT' });
		switchInstanceModalPage('tasks');
		return;
	}
	const accessLinksResult = InputValidation.instance.parseAccessLinksText(dom.instanceModalAccessLinks.value || '');
	if (accessLinksResult.error) {
		await showAlert(accessLinksResult.error, { title: 'INPUT' });
		switchInstanceModalPage('advanced');
		dom.instanceModalAccessLinks.focus();
		return;
	}
	payload.access_links = accessLinksResult.accessLinks;

	let createdInstance = null;
	if (modalState.mode === 'edit') {
		const editingName = modalState.editingInstanceName;
		if (!editingName) {
			await showAlert('当前没有可编辑的实例', { title: 'NOTICE' });
			return;
		}

		const updatedResult = await updateInstance(editingName, payload);
		if (!updatedResult.ok || !updatedResult.data) {
			if (updatedResult.unauthorized) {
				return;
			}
			await showAlert(updatedResult.error || '保存失败', { title: 'ERROR', tone: 'danger' });
			return;
		}
		const updated = updatedResult.data;
		const isEditingCurrentInstance = state.currentInstanceName === editingName;
		const oldPath = isEditingCurrentInstance ? pageState.currentTerminalSvc?.path : '';
		const newPath = updated.path ?? payload.path;
		const pathChanged = isEditingCurrentInstance && normalizeInstancePathForCompare(oldPath) !== normalizeInstancePathForCompare(newPath);
		const renamedCurrentInstance = isEditingCurrentInstance && updated.name !== editingName;

		if (pathChanged) {
			fileManager?.forgetLastDir?.(editingName);
			fileManager?.forgetLastDir?.(updated.name);
		} else {
			renameFileLastDirsInstance(editingName, updated.name);
		}

		if (renamedCurrentInstance) {
			state.currentInstanceName = updated.name;
			controller.updateUrl(updated.name);
		}
		patchCurrentTerminalInstance(updated);
		if (pathChanged) {
			await fileManager?.loadFiles('', 1, {
				instanceName: updated.name,
				instanceUpdateStagingDirName: pageState.currentInstanceUpdateStagingDirName,
			});
		}
	} else {
		const createdResult = await createInstance(payload);
		if (!createdResult.ok) {
			if (createdResult.unauthorized) {
				return;
			}
			await showAlert(createdResult.error || '创建失败', { title: 'ERROR', tone: 'danger' });
			return;
		}
		if (!createdResult.data || typeof createdResult.data.name !== 'string' || !createdResult.data.name.trim()) {
			await showAlert('创建失败: 接口未返回有效实例数据', { title: 'ERROR', tone: 'danger' });
			return;
		}
		createdInstance = {
			...createdResult.data,
			name: createdResult.data.name.trim(),
		};
	}

	closeInstanceModal();
	const loadedInstances = await controller.loadInstances();
	if (!Array.isArray(loadedInstances)) {
		await showAlert('刷新实例列表失败: 返回数据格式异常', { title: 'ERROR', tone: 'danger' });
		return;
	}
	cleanupFileLastDirs(loadedInstances.map((instance) => String(instance.name || '')));
	const targetName = createdInstance ? createdInstance.name : payload.name;
	const listInstance = loadedInstances.find(instance => instance.name === targetName);
	const ins = createdInstance ? { ...(listInstance || {}), ...createdInstance, name: targetName } : listInstance;
	if (ins) {
		controller.openInstanceTerminal(ins);
	}
	});
};

const showPage = () => {
    dom.section.classList.add('active');
	dom.navSep.classList.remove('hidden');
	dom.navCurrent.classList.remove('hidden');
	dom.navFileSep.classList.remove('hidden');
	dom.navFiles.classList.remove('hidden');
    dom.navHome.classList.add('clickable');
	dom.navCurrent.classList.add('clickable');
	dom.navFiles.classList.add('clickable');
};

const hidePage = () => {
    dom.section.classList.remove('active');
	dom.navSep.classList.add('hidden');
	dom.navCurrent.classList.add('hidden');
	dom.navFileSep.classList.add('hidden');
	dom.navFiles.classList.add('hidden');
    dom.navHome.classList.remove('clickable');
	dom.navCurrent.classList.remove('clickable');
	dom.navFiles.classList.remove('clickable');
};

const scrollTerminalPageToTop = () => {
	dom.section.scrollIntoView({ behavior: 'smooth', block: 'start' });
};

const scrollTerminalPageToFiles = () => {
	document.querySelector('#terminalSection .file-panel-card').scrollIntoView({ behavior: 'smooth', block: 'start' });
};

const bindTerminalEvents = () => {

	dom.navHome.onclick = () => {
		if (!state.currentInstanceName || !controller) {
			return;
		}
		void controller.leaveTerminalPage();
	};
	dom.navCurrent.onclick = () => {
		if (!state.currentInstanceName) {
			return;
		}
		scrollTerminalPageToTop();
	};
	dom.navFiles.onclick = () => {
		if (!state.currentInstanceName) {
			return;
		}
		scrollTerminalPageToFiles();
	};
	dom.instanceModalClose.onclick = () => closeInstanceModal();
	dom.instanceModalCancel.onclick = () => closeInstanceModal();
	dom.instanceModalForm.onsubmit = handleInstanceModalSubmit;
	bindInstanceModalTabs();
	bindGroupSelectEvents();
	if (dom.instanceTaskAdd) {
		dom.instanceTaskAdd.onclick = async () => {
			if (!dom.instanceTasksList) return;
			if (dom.instanceTasksList.querySelectorAll('.instance-task-row').length >= InputValidation.limits.tasksPerInstance) {
				await showAlert(`每个实例最多包含 ${InputValidation.limits.tasksPerInstance} 项计划任务`, { title: 'INPUT' });
				return;
			}
			dom.instanceTasksList.prepend(buildTaskRow({
				name: getUniqueTaskNameInModal(),
				enabled: true,
				expr: '',
				action: 'start',
				command: '',
			}));
			switchInstanceModalPage('tasks');
		};
	}
	if (dom.instanceTasksHelpClose) {
		dom.instanceTasksHelpClose.onclick = () => closeInstanceTasksHelpModal();
	}
	if (dom.instanceTasksHelpOk) {
		dom.instanceTasksHelpOk.onclick = () => closeInstanceTasksHelpModal();
	}
	if (dom.instanceTasksHelpModal) {
		dom.instanceTasksHelpModal.onclick = (event) => {
			if (event.target === dom.instanceTasksHelpModal) {
				closeInstanceTasksHelpModal();
			}
		};
	}
	if (dom.instanceModalAccessLinks) {
		dom.instanceModalAccessLinks.maxLength = InputValidation.limits.instanceAccessLinksText;
		dom.instanceModalAccessLinks.onblur = () => {
			dom.instanceModalAccessLinks.value = InputValidation.truncateText(dom.instanceModalAccessLinks.value || '', InputValidation.limits.instanceAccessLinksText);
		};
	}
	// Delete page uses the original modal submit button.
};

export const bootTerminalPage = (options = {}) => {
    controller = options.controller || null;
	fileSelection = createFileSelectionController();
	terminalCard = bootTerminalWorkspace({
		fileSelection,
		onEditInstance: (svc) => {
			if (svc) {
				openEditInstanceModal(svc);
			}
		},
		onToggleSelectAllCurrentDir: () => fileManager?.toggleSelectAllCurrentDir?.(),
	});
    fileEditorModal = bootFileEditorModal({
	        onRequestRefreshFiles: async () => {
	            await fileManager?.loadFiles();
	        },
	    });
    fileManager = bootFileManager({
        fileSelection,
        onOpenFileEditor: (file) => {
            fileEditorModal?.open(file);
        },
    });
    bindTerminalEvents();
	return {
        showPage,
        hidePage,
        closeTerminalPage,
        hasUnsavedFileEditorChanges: () => fileEditorModal ? fileEditorModal.hasUnsavedChanges() : false,
		tryLeaveCurrentContext: async () => fileEditorModal ? await fileEditorModal.tryClose() : true,
		loadFiles: (path, options) => fileManager ? fileManager.loadFiles(path, undefined, {
			...options,
			instanceUpdateStagingDirName: pageState.currentInstanceUpdateStagingDirName,
		}) : null,
		patchCurrentTerminalInstance,
		applyRuntimeSettings: applyRuntimeSettingsToTerminalPage,
		openCreateInstanceModal,
		openTerminalPage,
		prepareOpenTerminalPage,
	};
};
