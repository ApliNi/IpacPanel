
// https://lucide.dev/icons/

export const icons = {

	text: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-text-align-start-icon lucide-text-align-start"><path d="M21 5H3"/><path d="M15 12H3"/><path d="M17 19H3"/></svg>`,
		type: [
			'txt', 'md', 'markdown',
		],
		name: [
			'readme', 'license'
		],
	},

	list: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-list-icon lucide-list"><path d="M3 5h.01"/><path d="M3 12h.01"/><path d="M3 19h.01"/><path d="M8 5h13"/><path d="M8 12h13"/><path d="M8 19h13"/></svg>`,
		type: [
			'log', 'console_history'
		],
	},

	code: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-code-icon lucide-code"><path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/></svg>`,
		type: [
			'js', 'jsx', 'ts', 'tsx', 'vue', 'css', 'scss', 'less', 'html', 'htm',
			'py', 'java', 'c', 'cpp', 'h', 'hpp', 'cs', 'go', 'rs', 'rb', 'php',
			'swift', 'kt', 'sql', 'dart', 'lua',
		],
	},

	config: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-regex-icon lucide-regex"><path d="M17 3v10"/><path d="m12.67 5.5 8.66 5"/><path d="m12.67 10.5 8.66-5"/><path d="M9 17a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v2a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2v-2z"/></svg>`,
		type: [
			'json', 'jsonl', 'json5', 'xml', 'yaml', 'yml', 'toml', 'ini', 'conf', 'config',
			'env', 'properties', 'editorconfig', 'gitignore'
		],
		name: [
			'config',
		],
	},

	terminal: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-terminal-icon lucide-terminal"><path d="M12 19h8"/><path d="m4 17 6-6-6-6"/></svg>`,
		type: [
			'sh', 'bash', 'zsh', 'fish', 'cmd', 'bat', 'ps1', 'awk', 'make'
		],
		name: [
			'makefile'
		],
	},

	program: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-square-terminal-icon lucide-square-terminal"><path d="m7 11 2-2-2-2"/><path d="M11 13h4"/><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/></svg>`,
		type: [
			'exe', 'msi', 'apk', 'app', 'dmg', 'pkg', 'deb', 'rpm', 'jar',
			'bin', 'run', 'elf', 'com'
		],
	},

	database: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-database-icon lucide-database"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5V19A9 3 0 0 0 21 19V5"/><path d="M3 12A9 3 0 0 0 21 12"/></svg>`,
		type: [
			'db', 'sqlite', 'sqlite3'
		],
	},

	zip: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-folder-archive-icon lucide-folder-archive"><circle cx="15" cy="19" r="2"/><path d="M20.9 19.8A2 2 0 0 0 22 18V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h5.1"/><path d="M15 11v-1"/><path d="M15 17v-2"/></svg>`,
		type: [
			'zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'lzma', 'lz4',
			'zst', 'zstd', 'iso', 'cab', 'pkg'
		],
	},

	image: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-image-icon lucide-image"><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/></svg>`,
		type: [
			'jpg', 'jpeg', 'png', 'gif', 'svg', 'webp', 'ico', 'bmp',
			'avif', 'apng', 'heic', 'heif', 'tiff'
		],
	},

	audio: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-music4-icon lucide-music-4"><path d="M9 18V5l12-2v13"/><path d="m9 9 12-2"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`,
		type: [
			'mp3', 'wav', 'aac', 'm4a',
			'ogg', 'oga', 'opus', 'flac', 'mid', 'midi'
		],
	},

	video: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-play-icon lucide-play"><path d="M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z"/></svg>`,
		type: [
			'mp4', 'webm', 'ogv',
			'mov', 'm4v', '3gp'
		],
	},

	file: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" class="bi bi-file-earmark" viewBox="0 0 16 16"><path d="M14 4.5V14a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2a2 2 0 0 1 2-2h5.5zm-3 0A1.5 1.5 0 0 1 9.5 3V1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V4.5z"/></svg>`,
	},

	dir: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-folder-icon lucide-folder"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>`,
	},

	instanceUpdateDir: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-folder-up-icon lucide-folder-up"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/><path d="M12 10v6"/><path d="m9 13 3-3 3 3"/></svg>`,
	},

	instanceFileList: {
		icon: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-container-icon lucide-container"><path d="M22 7.7c0-.6-.4-1.2-.8-1.5l-6.3-3.9a1.72 1.72 0 0 0-1.7 0l-10.3 6c-.5.2-.9.8-.9 1.4v6.6c0 .5.4 1.2.8 1.5l6.3 3.9a1.72 1.72 0 0 0 1.7 0l10.3-6c.5-.3.9-1 .9-1.5Z"/><path d="M10 21.9V14L2.1 9.1"/><path d="m10 14 11.9-6.9"/><path d="M14 19.8v-8.1"/><path d="M18 17.5V9.4"/></svg>`,
	},

};

const fileTypeMap = {};
const fileNameMap = {};
const dirNameMap = {};

for (const iconName in icons) {
	const iconConfig = icons[iconName];
	if (!iconConfig.icon) {
		throw new Error(`图标 "${iconName}" 缺少 svg`);
	}
	for (const ext of iconConfig.type || []) {
		fileTypeMap[String(ext).toLowerCase()] = iconName;
	}
	for (const name of iconConfig.name || []) {
		fileNameMap[String(name).toLowerCase()] = iconName;
	}
	for (const name of iconConfig.dir || []) {
		dirNameMap[String(name).toLowerCase()] = iconName;
	}
}

export const getFileType = (name, dir = false) => {

	const getNormalizedName = (name) => String(name || '').trim().toLowerCase();

	const getFileExt = (name) => {
		const normalizedName = getNormalizedName(name);
		const dotIndex = normalizedName.lastIndexOf('.');
		if (dotIndex < 0 || dotIndex === normalizedName.length - 1) {
			return normalizedName;
		}
		return normalizedName.slice(dotIndex + 1);
	};

	const normalizedName = getNormalizedName(name);
	if (dir) {
		return dirNameMap[normalizedName] || 'dir';
	}
	return fileNameMap[normalizedName] || fileTypeMap[getFileExt(normalizedName)] || 'file';
};


const iconParser = new DOMParser();

export const cloneIconNode = (name) => {
	const iconConfig = icons[name] || icons.file;
	const source = iconConfig.icon;
	const doc = iconParser.parseFromString(source, 'image/svg+xml');
	const node = doc.documentElement;
	return document.importNode(node, true);
};

export const getFileIconNode = (name, dir = false, type = '') => {
	if (dir) {
		return cloneIconNode(icons[type] ? type : 'dir');
	}
	return cloneIconNode(getFileType(name, false));
};
