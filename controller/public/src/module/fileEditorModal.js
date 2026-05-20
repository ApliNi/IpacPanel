import { mainModalOverlay, state } from "../ui.js";
import { DEFAULT_UI_REFRESH_INTERVAL_MS, clearTimer, formatFileSize, withActionsDisabled } from '../utils/utils.js';
import { readFileContent, saveFileContentAdaptive } from '../api/file.js';
import { showAlert, showConfirm } from './dialog.js';
const runtimeConfig = {
	monacoVsPath: '/lib/monaco-editor',
};

const monacoWorkerAssets = new Map([
	['editor', '/lib/monaco-editor/assets/editor.worker-Be8ye1pW.js'],
	['editorWorkerService', '/lib/monaco-editor/assets/editor.worker-Be8ye1pW.js'],
	['typescript', '/lib/monaco-editor/assets/ts.worker-CMbG-7ft.js'],
	['javascript', '/lib/monaco-editor/assets/ts.worker-CMbG-7ft.js'],
	['json', '/lib/monaco-editor/assets/json.worker-DKiEKt88.js'],
	['css', '/lib/monaco-editor/assets/css.worker-HnVq6Ewq.js'],
	['scss', '/lib/monaco-editor/assets/css.worker-HnVq6Ewq.js'],
	['less', '/lib/monaco-editor/assets/css.worker-HnVq6Ewq.js'],
	['html', '/lib/monaco-editor/assets/html.worker-B51mlPHg.js'],
]);

const monacoNlsLanguages = new Set([
	'cs',
	'de',
	'es',
	'fr',
	'it',
	'ja',
	'ko',
	'pl',
	'pt-br',
	'ru',
	'tr',
	'zh-cn',
	'zh-tw',
]);

const normalizeBrowserLanguage = (language) => String(language || '').trim().toLowerCase().replace(/_/g, '-');

let monacoLanguageDefaultsConfigured = false;

const resolveMonacoNlsLanguage = () => {
	const win = getGlobalWindow();
	const nav = win.navigator || {};
	const languages = Array.isArray(nav.languages) && nav.languages.length > 0 ? nav.languages : [nav.language, nav.userLanguage];
	for (const rawLanguage of languages) {
		const language = normalizeBrowserLanguage(rawLanguage);
		if (!language) {
			continue;
		}
		if (language === 'zh' || language === 'zh-hans' || language.startsWith('zh-cn') || language.startsWith('zh-sg')) {
			return 'zh-cn';
		}
		if (language === 'zh-hant' || language.startsWith('zh-tw') || language.startsWith('zh-hk') || language.startsWith('zh-mo')) {
			return 'zh-tw';
		}
		if (monacoNlsLanguages.has(language)) {
			return language;
		}
		const baseLanguage = language.split('-')[0];
		if (monacoNlsLanguages.has(baseLanguage)) {
			return baseLanguage;
		}
	}
	return '';
};

const getMonacoNlsModuleLanguage = (language) => language ? `${language}.js` : '';

const getGlobalWindow = () => {
	if (typeof window === 'undefined') {
		throw new Error('Browser runtime is unavailable');
	}
	return window;
};

const resolveRuntimeAssetUrl = (path) => {
	const win = getGlobalWindow();
	const baseUrl = win.document?.baseURI || win.location?.href || '';
	return new URL(path, baseUrl).href;
};

const configureMonacoEnvironment = () => {
	const win = getGlobalWindow();
	const existing = win.MonacoEnvironment && typeof win.MonacoEnvironment === 'object' ? win.MonacoEnvironment : {};
	win.MonacoEnvironment = {
		...existing,
		getWorker(_moduleId, label) {
			const workerLabel = String(label || 'editor').trim() || 'editor';
			const workerPath = monacoWorkerAssets.get(workerLabel) || monacoWorkerAssets.get('editor');
			return new Worker(resolveRuntimeAssetUrl(workerPath), {
				type: 'module',
				name: workerLabel,
			});
		},
	};
};

const enableModeConfiguration = (defaults, configuration) => {
	defaults?.setModeConfiguration?.(configuration);
};

const configureMonacoLanguageDefaults = (monaco) => {
	if (monacoLanguageDefaultsConfigured || !monaco?.languages) {
		return;
	}
	monacoLanguageDefaultsConfigured = true;

	const jsonDefaults = monaco.languages.json?.jsonDefaults;
	jsonDefaults?.setDiagnosticsOptions?.({
		validate: true,
		allowComments: true,
		trailingCommas: 'ignore',
		enableSchemaRequest: false,
		schemas: [],
	});

	const ts = monaco.languages.typescript;
	if (ts) {
		const compilerOptions = {
			target: ts.ScriptTarget?.ES2022 ?? ts.ScriptTarget?.ES2020,
			module: ts.ModuleKind?.ESNext,
			moduleResolution: ts.ModuleResolutionKind?.NodeJs,
			allowNonTsExtensions: true,
			allowJs: true,
			checkJs: false,
			noEmit: true,
			strict: false,
			jsx: ts.JsxEmit?.ReactJSX ?? ts.JsxEmit?.React,
		};
		const diagnosticsOptions = {
			noSemanticValidation: false,
			noSyntaxValidation: false,
		};
		ts.typescriptDefaults?.setCompilerOptions?.(compilerOptions);
		ts.javascriptDefaults?.setCompilerOptions?.(compilerOptions);
		ts.typescriptDefaults?.setDiagnosticsOptions?.(diagnosticsOptions);
		ts.javascriptDefaults?.setDiagnosticsOptions?.(diagnosticsOptions);
		ts.typescriptDefaults?.setEagerModelSync?.(true);
		ts.javascriptDefaults?.setEagerModelSync?.(true);
	}

	const cssModeConfiguration = {
		completionItems: true,
		hovers: true,
		documentSymbols: true,
		definitions: true,
		references: true,
		documentHighlights: true,
		documentLinks: true,
		foldingRanges: true,
		diagnostics: true,
		selectionRanges: true,
		colors: true,
		documentFormattingEdits: true,
		documentRangeFormattingEdits: true,
	};
	for (const defaults of [
		monaco.languages.css?.cssDefaults,
		monaco.languages.css?.scssDefaults,
		monaco.languages.css?.lessDefaults,
	]) {
		defaults?.setDiagnosticsOptions?.({ validate: true });
		enableModeConfiguration(defaults, cssModeConfiguration);
	}

	const htmlDefaults = monaco.languages.html?.htmlDefaults;
	htmlDefaults?.setOptions?.({
		format: {
			tabSize: 4,
			insertSpaces: false,
			wrapLineLength: 120,
			unformatted: 'default',
		},
	});
	enableModeConfiguration(htmlDefaults, {
		completionItems: true,
		hovers: true,
		documentHighlights: true,
		documentLinks: true,
		documentSymbols: true,
		foldingRanges: true,
		selectionRanges: true,
		diagnostics: true,
		documentFormattingEdits: true,
		documentRangeFormattingEdits: true,
	});
};

const getMonacoVsBaseUrl = () => String(runtimeConfig.monacoVsPath).replace(/\/+$/, '');

const getMonacoRuntime = () => {
	const win = getGlobalWindow();
	return win.monaco && win.monaco.editor ? win.monaco : null;
};

const getMonacoAmdLoader = () => {
	const win = getGlobalWindow();
	return typeof win.require === 'function' && typeof win.require.config === 'function' ? win.require : null;
};

const ensureMonacoRuntime = (() => {
	let monacoLoadPromise = null;
	return async () => {
		const existing = getMonacoRuntime();
		if (existing) {
			return existing;
		}
		if (monacoLoadPromise) {
			return monacoLoadPromise;
		}
		monacoLoadPromise = new Promise((resolve, reject) => {
			const amdRequire = getMonacoAmdLoader();
			if (!amdRequire) {
				reject(new Error('Monaco AMD loader is unavailable'));
				return;
			}
			configureMonacoEnvironment();
			const nlsLanguage = resolveMonacoNlsLanguage();
			const monacoLoaderConfig = {
				paths: { vs: getMonacoVsBaseUrl() },
			};
			if (nlsLanguage) {
				monacoLoaderConfig['vs/nls'] = { availableLanguages: { '*': getMonacoNlsModuleLanguage(nlsLanguage) } };
			}
			amdRequire.config(monacoLoaderConfig);
			amdRequire([
				'vs/editor/editor.main',
			], () => {
				const monaco = getMonacoRuntime();
				if (monaco) {
					configureMonacoLanguageDefaults(monaco);
					resolve(monaco);
					return;
				}
				reject(new Error('Monaco loaded but runtime is unavailable'));
			}, (err) => {
				reject(err || new Error('Failed to load Monaco runtime'));
			});
		});
		try {
			return await monacoLoadPromise;
		} catch (error) {
			monacoLoadPromise = null;
			throw error;
		}
	};
})();

console.log('[模块] FileEditorModal 加载中...');

const getReadableErrorMessage = (error, fallback = 'Unknown error') => {
	if (!error) {
		return fallback;
	}
	if (typeof error === 'string') {
		const text = error.trim();
		return text || fallback;
	}
	if (typeof error?.message === 'string') {
		const text = error.message.trim();
		if (text && text !== '[object Event]') {
			return text;
		}
	}
	const eventType = typeof error?.type === 'string' ? error.type.trim() : '';
	const targetSrc = typeof error?.target?.src === 'string' ? error.target.src.trim() : '';
	if (eventType || targetSrc) {
		const details = [];
		if (eventType) {
			details.push(`type=${eventType}`);
		}
		if (targetSrc) {
			details.push(`src=${targetSrc}`);
		}
		return `Resource load failed${details.length ? ` (${details.join(', ')})` : ''}`;
	}
	const text = String(error).trim();
	if (!text || text === '[object Event]') {
		return fallback;
	}
	return text;
};

const buildModalHeaderNode = ({ title, closeId = '', titleId = '' } = {}) => {
	const header = document.createElement('div');
	header.className = 'modal-header';

	const titleNode = document.createElement('span');
	titleNode.className = 'modal-title';
	titleNode.textContent = title || '';
	if (titleId) {
		titleNode.id = titleId;
	}

	const closeButton = document.createElement('button');
	closeButton.className = 'modal-close';
	closeButton.type = 'button';
	closeButton.textContent = '×';
	if (closeId) {
		closeButton.id = closeId;
	}

	header.append(titleNode, closeButton);
	return header;
};

const buildFieldGroupNode = ({ label, content, className = '' } = {}) => {
	const group = document.createElement('div');
	group.className = ['field-group', className].filter(Boolean).join(' ');
	if (label !== undefined) {
		const labelNode = document.createElement('span');
		labelNode.textContent = label || '';
		group.appendChild(labelNode);
	}
	if (content instanceof Node) {
		group.appendChild(content);
	}
	return group;
};

const buildModalActionsNode = ({ id = '', className = '', content } = {}) => {
	const actions = document.createElement('div');
	actions.className = ['modal-actions', className].filter(Boolean).join(' ');
	if (id) {
		actions.id = id;
	}
	if (Array.isArray(content)) {
		actions.append(...content);
	}
	return actions;
};

const fileEditorModal = document.createElement('div');
fileEditorModal.id = 'fileEditorModal';
fileEditorModal.className = 'modal-overlay';

const fileEditorCard = document.createElement('div');
fileEditorCard.className = 'modal-card file-editor-modal-card';

const fileEditorForm = document.createElement('form');
fileEditorForm.id = 'fileEditorForm';
fileEditorForm.className = 'modal-form file-editor-form';

const fileEditorMonaco = document.createElement('div');
fileEditorMonaco.id = 'fileEditorMonaco';
fileEditorMonaco.className = 'file-editor-monaco hidden';

const fileEditorStatus = document.createElement('span');
fileEditorStatus.id = 'fileEditorStatus';
fileEditorStatus.ariaLive = 'polite';

const fileEditorToggleDiff = document.createElement('button');
fileEditorToggleDiff.className = 'btn';
fileEditorToggleDiff.id = 'fileEditorToggleDiff';
fileEditorToggleDiff.type = 'button';
fileEditorToggleDiff.textContent = 'DIFF';

const controlsDivider = document.createElement('span');
controlsDivider.className = 'controls-divider';
controlsDivider.ariaHidden = 'true';
controlsDivider.textContent = '|';

const fileEditorCancel = document.createElement('button');
fileEditorCancel.className = 'btn';
fileEditorCancel.id = 'fileEditorCancel';
fileEditorCancel.type = 'button';
fileEditorCancel.textContent = 'CANCEL';

const fileEditorSave = document.createElement('button');
fileEditorSave.className = 'btn btn-start';
fileEditorSave.type = 'submit';
fileEditorSave.textContent = 'SAVE';

fileEditorForm.append(
	buildFieldGroupNode({ content: fileEditorMonaco }),
	buildModalActionsNode({
		content: [fileEditorStatus, fileEditorToggleDiff, controlsDivider, fileEditorCancel, fileEditorSave],
	}),
);
fileEditorCard.append(buildModalHeaderNode({ title: 'EDIT FILE', closeId: 'fileEditorClose', titleId: 'fileEditorTitle' }), fileEditorForm);
fileEditorModal.appendChild(fileEditorCard);
mainModalOverlay?.appendChild(fileEditorModal);

const dom = {
	fileEditorModal: document.getElementById('fileEditorModal'),
	fileEditorForm: document.getElementById('fileEditorForm'),
	fileEditorClose: document.getElementById('fileEditorClose'),
	fileEditorCancel: document.getElementById('fileEditorCancel'),
	fileEditorToggleDiff: document.getElementById('fileEditorToggleDiff'),
	fileEditorTitle: document.getElementById('fileEditorTitle'),
	fileEditorStatus: document.getElementById('fileEditorStatus'),
	fileEditorMonaco: document.getElementById('fileEditorMonaco'),
	fileEditorActions: document.querySelector('#fileEditorForm .modal-actions'),
};

const editorState = {
    fileEditorModalCloseTimer: null,
    editingFilePath: '',
    originalFileContent: '',
    fileEditorMonacoInitSeq: 0,
    fileEditorOpSeq: 0,
    fileEditorLastError: '',
    fileEditorSaving: false,
	fileEditorSaveAbortController: null,
	allowLargeFileRead: false,
    fileEditorViewMode: 'edit',
    fileEditorStatusMode: 'idle',
    fileEditorBaselineLines: 0,
    fileEditorBaselineBytes: 0,
    fileEditorContentLoaded: false,
	pendingInitialContent: null,
	fileEditorMetricsTimer: null,
	fileEditorSuccessTimer: null,
	monacoEditor: null,
	monacoDiffEditor: null,
	monacoOriginalModel: null,
	monacoModel: null,
    monacoModelChangeDisposable: null,
    monacoPreloadPromise: null,
    onRequestRefreshFiles: null,
    isBound: false,
};

const getFileEditorText = () => {
    // In diff mode, monacoEditor is disposed; monacoModel still holds modified content.
    if (editorState.monacoModel && typeof editorState.monacoModel.getValue === 'function') {
        return editorState.monacoModel.getValue() || '';
    }
    if (editorState.monacoEditor && typeof editorState.monacoEditor.getValue === 'function') {
        return editorState.monacoEditor.getValue() || '';
    }
    return editorState.originalFileContent || '';
};

const loadMonaco = async () => {
    return await ensureMonacoRuntime();
};

const preloadMonaco = () => {
    if (editorState.monacoPreloadPromise) {
        return editorState.monacoPreloadPromise;
    }
    editorState.monacoPreloadPromise = loadMonaco().then((monaco) => {
        try {
            // Warm up a tiny model to reduce first-open jank.
            const uri = monaco.Uri.parse('inmemory://IpacPanel/__warmup__.txt');
            const model = monaco.editor.createModel('', undefined, uri);
            model.dispose();
        } catch (_) {}
        return monaco;
    }).catch((err) => {
        // Preload is best-effort; open() will still surface errors if Monaco can't load.
        console.warn('[模块] Monaco 预加载失败:', err);
        return null;
    });
    return editorState.monacoPreloadPromise;
};

const createFileLikeUri = (monaco, pathOrName) => {
    const raw = String(pathOrName || 'file').trim();
    const normalized = raw.replace(/\\/g, '/').replace(/^\/+/, '');
    const safePath = normalized
        .split('/')
        .filter(Boolean)
        .map((seg) => encodeURIComponent(seg))
        .join('/');
    return monaco.Uri.parse(`file:///${safePath || 'file'}`);
};

const setFileEditorStatus = (text, options = {}) => {
    if (!dom.fileEditorStatus) return;
    const message = String(text || '').trim();
    dom.fileEditorStatus.textContent = message;
    dom.fileEditorStatus.classList.toggle('error', !!options.error);
};

const countTextLines = (text) => {
    const value = String(text || '');
    if (!value) return 0;
    return value.split('\n').length;
};

const getUtf8ByteLength = (text) => {
    try {
        return new TextEncoder().encode(String(text || '')).length;
    } catch (_) {
        return new Blob([String(text || '')]).size;
    }
};

const renderFileEditorMetricsStatus = () => {
    if (editorState.fileEditorStatusMode !== 'ready') {
        return;
    }
    if (!editorState.monacoEditor) {
        return;
    }
    const text = editorState.monacoEditor.getValue() || '';
    const currentLines = countTextLines(text);
    const currentBytes = getUtf8ByteLength(text);
    const fromLines = editorState.fileEditorBaselineLines || 0;
    const fromBytes = editorState.fileEditorBaselineBytes || 0;
    setFileEditorStatus(`[${fromLines} Line / ${formatFileSize(fromBytes)}] -> [${currentLines} Line / ${formatFileSize(currentBytes)}]`);
};

const scheduleFileEditorMetricsStatus = () => {
    editorState.fileEditorMetricsTimer = clearTimer(editorState.fileEditorMetricsTimer);
    editorState.fileEditorMetricsTimer = setTimeout(() => {
        editorState.fileEditorMetricsTimer = null;
        renderFileEditorMetricsStatus();
	}, DEFAULT_UI_REFRESH_INTERVAL_MS);
};

const setFileEditorStatusMode = (mode, message = '') => {
    editorState.fileEditorStatusMode = mode || 'idle';
    if (editorState.fileEditorStatusMode === 'ready') {
        renderFileEditorMetricsStatus();
        return;
    }
    if (!message) {
        setFileEditorStatus('');
        return;
    }
    setFileEditorStatus(message, { error: editorState.fileEditorStatusMode === 'error' });
};

const showFileEditorSaveSuccess = () => {
    editorState.fileEditorSuccessTimer = clearTimer(editorState.fileEditorSuccessTimer);
    setFileEditorStatusMode('success', '保存成功');
    editorState.fileEditorSuccessTimer = setTimeout(() => {
        editorState.fileEditorSuccessTimer = null;
        if (!dom.fileEditorModal || dom.fileEditorModal.style.display === 'none') {
            return;
        }
        if (editorState.fileEditorStatusMode !== 'success') {
            return;
        }
        setFileEditorStatusMode('ready');
    }, 1000);
};

const setFileEditorMode = () => {
    dom.fileEditorMonaco?.classList.remove('hidden');
};

const disposeMonacoEditor = () => {
    try {
        editorState.monacoModelChangeDisposable?.dispose?.();
    } catch (_) {}
    editorState.monacoModelChangeDisposable = null;
    try {
        editorState.monacoEditor?.dispose?.();
    } catch (_) {}
    editorState.monacoEditor = null;
    try {
        editorState.monacoDiffEditor?.dispose?.();
    } catch (_) {}
    editorState.monacoDiffEditor = null;
    try {
        editorState.monacoModel?.dispose?.();
    } catch (_) {}
    editorState.monacoModel = null;
    try {
        editorState.monacoOriginalModel?.dispose?.();
    } catch (_) {}
    editorState.monacoOriginalModel = null;
};

const applyInitialContentToModel = (content) => {
    const text = String(content ?? '');
    if (!editorState.monacoModel || typeof editorState.monacoModel.setValue !== 'function') {
        editorState.pendingInitialContent = text;
        return;
    }
    editorState.monacoModel.setValue(text);
};

const setEditorContentLoaded = (loaded, message) => {
    editorState.fileEditorContentLoaded = !!loaded;
    if (!loaded) {
        setFileEditorStatusMode('loading', message || 'LOADING...');
        return;
    }
    setFileEditorStatusMode('ready');
};

const syncFileEditorBaseline = (content) => {
    const text = String(content ?? '');
    editorState.originalFileContent = text;
    editorState.fileEditorBaselineLines = countTextLines(text);
    editorState.fileEditorBaselineBytes = getUtf8ByteLength(text);
};

const syncFileEditorBaselineFromModel = () => {
    syncFileEditorBaseline(getFileEditorText());
};

const loadFileContentIntoEditor = async (path, name, opSeq) => {
	const instanceName = state.currentInstanceName;
	if (!instanceName || !path) {
		return;
	}
	try {
		const fileResult = await readFileContent(instanceName, path, { allowLarge: editorState.allowLargeFileRead === true });
        if (opSeq !== editorState.fileEditorOpSeq) return;
		if (!fileResult.ok || !fileResult.data) {
			editorState.fileEditorLastError = fileResult.error || '读取文件失败';
			if (!fileResult.unauthorized) {
				await showAlert(`读取文件失败: ${editorState.fileEditorLastError}`, { title: 'ERROR', tone: 'danger' });
			}
			setFileEditorStatusMode('error', `加载失败: ${editorState.fileEditorLastError}`);
			return;
		}
		const file = fileResult.data;

        const fileContent = file.content || '';
        editorState.editingFilePath = file.path || path;
        syncFileEditorBaseline(fileContent);

        applyInitialContentToModel(fileContent);
        syncFileEditorBaselineFromModel();
        // If Monaco editor is already created, wire up metrics & dirty tracking now.
        if (editorState.monacoModel) {
            setFileEditorModelChangeHandler();
        }
        setEditorContentLoaded(true);
        try {
            editorState.monacoEditor?.updateOptions?.({ readOnly: false });
        } catch (_) {}
	} catch (error) {
        if (opSeq !== editorState.fileEditorOpSeq) return;
		editorState.fileEditorLastError = getReadableErrorMessage(error, 'Failed to load file content');
		setFileEditorStatusMode('error', `加载失败: ${editorState.fileEditorLastError}`);
	}
};

const updateFileEditorToggleDiffButton = () => {
    if (!dom.fileEditorToggleDiff) return;
    const mode = editorState.fileEditorViewMode || 'edit';
    dom.fileEditorToggleDiff.textContent = mode === 'diff' ? 'EDIT' : 'DIFF';
};

const setFileEditorViewMode = (mode) => {
    editorState.fileEditorViewMode = mode === 'diff' ? 'diff' : 'edit';
    updateFileEditorToggleDiffButton();
};

const setFileEditorModelChangeHandler = () => {
    try {
        editorState.monacoModelChangeDisposable?.dispose?.();
    } catch (_) {}
    editorState.monacoModelChangeDisposable = null;
    if (!editorState.monacoModel?.onDidChangeContent) {
        return;
    }
    editorState.monacoModelChangeDisposable = editorState.monacoModel.onDidChangeContent(() => {
        if (editorState.fileEditorStatusMode === 'saving' || editorState.fileEditorStatusMode === 'loading') {
            return;
        }
        if (editorState.fileEditorStatusMode === 'error') {
            editorState.fileEditorStatusMode = 'ready';
        }
        scheduleFileEditorMetricsStatus();
    });
};

const focusCurrentFileEditor = () => {
    if (editorState.fileEditorViewMode === 'diff') {
        editorState.monacoDiffEditor?.getModifiedEditor?.()?.focus?.();
        return;
    }
    editorState.monacoEditor?.focus?.();
};

const saveFileEditorContent = async (options = {}) => {
	const { closeOnSuccess = false, refreshFileList = false } = options;
	const instanceName = state.currentInstanceName;
	if (!instanceName) {
		return false;
	}
    if (editorState.fileEditorSaving) {
        return false;
    }
	if (!editorState.fileEditorContentLoaded) {
		return false;
	}
	if (!editorState.monacoModel || typeof editorState.monacoModel.getValue !== 'function') {
		console.warn('[控制台页] Monaco 尚未就绪, 无法保存');
		return false;
	}

    const path = editorState.editingFilePath;
    const content = getFileEditorText();
    if (!path) {
        return false;
    }

    editorState.fileEditorSaving = true;
    const saveAbortController = new AbortController();
    editorState.fileEditorSaveAbortController = saveAbortController;
    setFileEditorStatusMode('saving', '正在保存...');
	try {
		const result = await saveFileContentAdaptive(instanceName, path, content, { signal: saveAbortController.signal });
		if (!result.ok) {
			const message = result.error || 'Unknown error';
			if (message === 'aborted') {
				setFileEditorStatusMode('error', '保存已取消');
				return false;
			}
			setFileEditorStatusMode('error', `保存失败: ${message}`);
			return false;
		}
	} catch (error) {
		if (error && error.name === 'AbortError') {
			setFileEditorStatusMode('error', '保存已取消');
			return false;
		}
		const message = getReadableErrorMessage(error, 'Failed to save file content');
		setFileEditorStatusMode('error', `保存失败: ${message}`);
		return false;
	} finally {
        if (editorState.fileEditorSaveAbortController === saveAbortController) {
            editorState.fileEditorSaveAbortController = null;
        }
        editorState.fileEditorSaving = false;
    }

	syncFileEditorBaseline(content);
	// Keep diff view in sync: after save, original should match modified.
	try {
		editorState.monacoOriginalModel?.setValue?.(content);
	} catch (_) {}
	if (refreshFileList && typeof editorState.onRequestRefreshFiles === 'function') {
		await editorState.onRequestRefreshFiles();
	}
	showFileEditorSaveSuccess();
	if (closeOnSuccess) {
		closeFileEditorModal();
    }
    return true;
};

const bindFileEditorSaveCommands = (monaco, editorInstance) => {
    if (!editorInstance || typeof editorInstance.addCommand !== 'function') return;
    editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, async () => {
        await saveFileEditorContent({ closeOnSuccess: false, refreshFileList: false });
    });
    editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyS, async () => {
        await saveFileEditorContent({ closeOnSuccess: false, refreshFileList: false });
    });
};

const applyFileEditorViewMode = async (mode) => {
    if (!editorState.monacoModel) {
        return;
    }
    const monaco = getMonacoRuntime();
    if (!monaco || !monaco.editor) {
        return;
    }
    if (!dom.fileEditorMonaco) {
        return;
    }

    const nextMode = mode === 'diff' ? 'diff' : 'edit';
    if (nextMode === editorState.fileEditorViewMode) {
        return;
    }

    if (nextMode === 'diff') {
        if (!editorState.monacoOriginalModel) {
            const uri = createFileLikeUri(monaco, `${editorState.editingFilePath || 'file'}.__original__`);
            editorState.monacoOriginalModel = monaco.editor.createModel(editorState.originalFileContent || '', undefined, uri);
        }

        try {
            editorState.monacoEditor?.dispose?.();
        } catch (_) {}
        editorState.monacoEditor = null;
		dom.fileEditorMonaco.replaceChildren();

        editorState.monacoDiffEditor = monaco.editor.createDiffEditor(dom.fileEditorMonaco, {
            automaticLayout: true,
            renderSideBySide: true,
            readOnly: false,
            scrollBeyondLastLine: false,
            minimap: { enabled: false },
            fontFamily: `"JetBrains Mono", "JetBrains Maple Mono Regular", "JetBrains Maple Mono"`,
            fontSize: 12,
            lineHeight: 18,
            wordWrap: 'on',
            renderWhitespace: 'selection',
            originalEditable: false,
        });
        editorState.monacoDiffEditor.setModel({
            original: editorState.monacoOriginalModel,
            modified: editorState.monacoModel,
        });

        bindFileEditorSaveCommands(monaco, editorState.monacoDiffEditor.getModifiedEditor());
        setFileEditorViewMode('diff');
        requestAnimationFrame(() => {
            editorState.monacoDiffEditor?.layout?.();
            focusCurrentFileEditor();
        });
        return;
    }

    try {
        editorState.monacoDiffEditor?.dispose?.();
    } catch (_) {}
    editorState.monacoDiffEditor = null;
	dom.fileEditorMonaco.replaceChildren();

    editorState.monacoEditor = monaco.editor.create(dom.fileEditorMonaco, {
        model: editorState.monacoModel,
        automaticLayout: true,
        scrollBeyondLastLine: false,
        minimap: { enabled: false },
        fontFamily: `"JetBrains Mono", "JetBrains Maple Mono Regular", "JetBrains Maple Mono"`,
        fontSize: 12,
        lineHeight: 18,
        tabSize: 4,
        insertSpaces: false,
        wordWrap: 'on',
        renderWhitespace: 'selection',
        roundedSelection: false,
        scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
        theme: 'vs-dark',
    });
    bindFileEditorSaveCommands(monaco, editorState.monacoEditor);
    setFileEditorModelChangeHandler();
    setFileEditorViewMode('edit');
    requestAnimationFrame(() => {
        editorState.monacoEditor?.layout?.();
        focusCurrentFileEditor();
    });
};

const isFileEditorDirty = () => {
    const original = editorState.originalFileContent || '';
    return getFileEditorText() !== original;
};

const confirmCloseDirtyFileEditor = async () => {
    if (!isFileEditorDirty()) {
        return true;
    }
    return await showConfirm('当前内容尚未保存, 确定离开吗?', {
        title: 'CONFIRM',
        okText: 'OK',
        cancelText: 'CANCEL',
        tone: 'warning',
    });
};

const closeFileEditorModal = () => {
    if (!dom.fileEditorModal) return;
    if (editorState.fileEditorSaveAbortController) {
        editorState.fileEditorSaveAbortController.abort();
        editorState.fileEditorSaveAbortController = null;
    }
    editorState.fileEditorMonacoInitSeq += 1;
    editorState.fileEditorOpSeq += 1;
    editorState.fileEditorMetricsTimer = clearTimer(editorState.fileEditorMetricsTimer);
    editorState.fileEditorSuccessTimer = clearTimer(editorState.fileEditorSuccessTimer);
    disposeMonacoEditor();
    editorState.fileEditorBaselineLines = 0;
    editorState.fileEditorBaselineBytes = 0;
    editorState.fileEditorContentLoaded = false;
	editorState.allowLargeFileRead = false;
    editorState.pendingInitialContent = null;
    editorState.fileEditorViewMode = 'edit';
    setFileEditorStatusMode('idle', '');
    updateFileEditorToggleDiffButton();
    dom.fileEditorModal.classList.remove('visible');
    dom.fileEditorModal.classList.add('closing');
    editorState.fileEditorModalCloseTimer = setTimeout(() => {
        dom.fileEditorModal.style.display = 'none';
        dom.fileEditorModal.classList.remove('closing');
        editorState.fileEditorModalCloseTimer = null;
        editorState.editingFilePath = '';
        editorState.originalFileContent = '';
    }, 280);
};

const openFileEditorModal = (file) => {
    if (!dom.fileEditorModal || !dom.fileEditorTitle) return;
    editorState.fileEditorOpSeq += 1;
    const opSeq = editorState.fileEditorOpSeq;
    editorState.fileEditorMetricsTimer = clearTimer(editorState.fileEditorMetricsTimer);
    editorState.fileEditorSuccessTimer = clearTimer(editorState.fileEditorSuccessTimer);
    editorState.fileEditorModalCloseTimer = clearTimer(editorState.fileEditorModalCloseTimer);
	const openPath = file && file.path ? file.path : '';
	const openName = file && file.name ? file.name : '';
	const hasInlineContent = file && typeof file.content === 'string';
	editorState.allowLargeFileRead = file && file.allowLarge === true;
    editorState.editingFilePath = openPath;
    syncFileEditorBaseline(hasInlineContent ? (file.content || '') : '');
    editorState.pendingInitialContent = null;
    editorState.fileEditorContentLoaded = hasInlineContent;
    editorState.fileEditorViewMode = 'edit';
    dom.fileEditorTitle.innerText = openName || 'Editor';
    setFileEditorMode();
    setEditorContentLoaded(hasInlineContent, '正在读取文件...');
    updateFileEditorToggleDiffButton();
    dom.fileEditorModal.style.display = 'flex';
    dom.fileEditorModal.classList.remove('closing');
    requestAnimationFrame(() => {
        dom.fileEditorModal.classList.add('visible');
    });

    editorState.fileEditorMonacoInitSeq += 1;
    const seq = editorState.fileEditorMonacoInitSeq;
    const fileName = openName || '';
    const fileContent = hasInlineContent ? (file.content || '') : '';
    const filePath = openPath || '';

    // Fire network request immediately; Monaco init happens in parallel.
    if (!hasInlineContent && filePath) {
        loadFileContentIntoEditor(filePath, fileName, opSeq);
    }

    window.setTimeout(async () => {
        if (opSeq !== editorState.fileEditorOpSeq) return;
        if (seq !== editorState.fileEditorMonacoInitSeq) return;
        if (!dom.fileEditorModal || dom.fileEditorModal.style.display === 'none') return;
        if (!dom.fileEditorMonaco) return;
        try {
            const monaco = await loadMonaco();
            if (seq !== editorState.fileEditorMonacoInitSeq) return;
            if (opSeq !== editorState.fileEditorOpSeq) return;
            if (!dom.fileEditorModal || dom.fileEditorModal.style.display === 'none') return;

            disposeMonacoEditor();
            const uri = createFileLikeUri(monaco, filePath || fileName);
            const initialText = editorState.pendingInitialContent != null ? editorState.pendingInitialContent : fileContent;
            editorState.monacoModel = monaco.editor.createModel(initialText, undefined, uri);
            syncFileEditorBaselineFromModel();
            editorState.monacoEditor = monaco.editor.create(dom.fileEditorMonaco, {
                model: editorState.monacoModel,
                automaticLayout: true,
                readOnly: !editorState.fileEditorContentLoaded,
                scrollBeyondLastLine: false,
                minimap: { enabled: false },
                fontFamily: `"JetBrains Mono", "JetBrains Maple Mono Regular", "JetBrains Maple Mono"`,
                fontSize: 12,
                lineHeight: 18,
                tabSize: 4,
                insertSpaces: false,
                wordWrap: 'on',
                renderWhitespace: 'selection',
                roundedSelection: false,
                scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
                theme: 'vs-dark',
            });

            if (editorState.fileEditorContentLoaded) {
                setFileEditorStatusMode('ready');
                setFileEditorModelChangeHandler();
            }
            setFileEditorViewMode('edit');
            bindFileEditorSaveCommands(monaco, editorState.monacoEditor);

			requestAnimationFrame(() => {
				editorState.monacoEditor?.layout?.();
				focusCurrentFileEditor();
			});
		} catch (error) {
			editorState.fileEditorLastError = getReadableErrorMessage(error, 'Failed to initialize Monaco editor');
			setFileEditorStatusMode('error', `加载失败: ${editorState.fileEditorLastError}`);
		}
	}, 0);
};

const tryCloseFileEditorModal = async () => {
    if (!await confirmCloseDirtyFileEditor()) {
        return false;
    }
    closeFileEditorModal();
    return true;
};

const hasUnsavedFileEditorChanges = () => {
    if (!dom.fileEditorModal || dom.fileEditorModal.style.display === 'none') {
        return false;
    }
    return isFileEditorDirty();
};

const bindModalEvents = () => {
    if (editorState.isBound) {
        return;
    }
    editorState.isBound = true;

    if (dom.fileEditorClose) {
        dom.fileEditorClose.onclick = () => void tryCloseFileEditorModal();
    }
    if (dom.fileEditorCancel) {
        dom.fileEditorCancel.onclick = () => void tryCloseFileEditorModal();
    }
	if (dom.fileEditorForm) {
		dom.fileEditorForm.onsubmit = async (event) => {
			event.preventDefault();
			await withActionsDisabled(dom.fileEditorActions, async () => {
				await saveFileEditorContent({ closeOnSuccess: true, refreshFileList: true });
			});
		};
	}
    if (dom.fileEditorToggleDiff) {
        dom.fileEditorToggleDiff.onclick = async () => {
            if (!editorState.fileEditorContentLoaded) {
                return;
            }
            if (!editorState.monacoModel) {
                return;
            }
            const nextMode = editorState.fileEditorViewMode === 'diff' ? 'edit' : 'diff';
            await applyFileEditorViewMode(nextMode);
        };
    }
};

export const bootFileEditorModal = (options = {}) => {
    editorState.onRequestRefreshFiles = options.onRequestRefreshFiles || null;
    bindModalEvents();
    preloadMonaco();
    return {
        open: openFileEditorModal,
        close: closeFileEditorModal,
        tryClose: tryCloseFileEditorModal,
        hasUnsavedChanges: hasUnsavedFileEditorChanges,
    };
};
