import { mainModalOverlay } from "../ui.js";
import { DEFAULT_UI_REFRESH_INTERVAL_MS } from '../utils/utils.js';

mainModalOverlay.insertAdjacentHTML('beforeend', /*html*/`
	<div id="mediaPreview">
		<img src="" alt="image" draggable="false">
		<video playsinline controls preload="metadata"></video>
		<audio controls preload="metadata"></audio>
	</div>
`);

const previewBox = document.getElementById('mediaPreview');
const img = previewBox ? previewBox.querySelector('img') : null;
const video = previewBox ? previewBox.querySelector('video') : null;
const audio = previewBox ? previewBox.querySelector('audio') : null;

let currentPreviewUrl = '';
let currentPreviewType = 'img';

let scale = 1;
let closeTimer = null;

const clearCloseTimer = () => {
	if (closeTimer) {
		clearTimeout(closeTimer);
		closeTimer = null;
	}
};

const ensureImgFadeTransition = () => {
	if (!img) return;
	// 拖拽时可能会把 transition 写成仅 transform / none，导致关闭时 opacity 无渐变
	img.style.transition = 'transform 0.4s ease, top 0.4s ease, left 0.4s ease, opacity 0.4s ease';
};

const closePreview = () => {
	if (!previewBox) return;
	if (!previewBox.classList.contains('open')) return;

	clearCloseTimer();
	if (currentPreviewType === 'img') {
		ensureImgFadeTransition();
	}
	previewBox.classList.add('closing');
	closeTimer = setTimeout(() => {
		if (video) {
			video.pause();
			video.removeAttribute('src');
			video.load();
		}
		if (audio) {
			audio.pause();
			audio.removeAttribute('src');
			audio.load();
		}
		if (img) {
			img.src = '';
		}
		previewBox.classList.remove('open');
		previewBox.classList.remove('closing');
		previewBox.dataset.previewType = '';
		closeTimer = null;
	}, DEFAULT_UI_REFRESH_INTERVAL_MS);
};

const clearPreviewUrl = () => {
	if (currentPreviewUrl && currentPreviewUrl.startsWith('blob:')) {
		try { URL.revokeObjectURL(currentPreviewUrl); } catch { /* ignore */ }
	}
	currentPreviewUrl = '';
};

const setPreviewType = (type) => {
	currentPreviewType = type;
	if (previewBox) {
		previewBox.dataset.previewType = type;
	}
	if (img) {
		img.classList.toggle('active', type === 'img');
	}
	if (video) {
		video.classList.toggle('active', type === 'video');
	}
	if (audio) {
		audio.classList.toggle('active', type === 'audio');
	}
};

export const openPreview = async (src, type = 'img') => {
	if (!previewBox || !img || !video || !audio) return;
	clearCloseTimer();
	previewBox.classList.remove('closing');
	previewBox.classList.add('open');
	scale = 1;
	img.style.transform = 'scale(1)';
	img.style.left = '';
	img.style.top = '';
	clearPreviewUrl();
	setPreviewType(type);
	img.src = '';
	video.pause();
	video.removeAttribute('src');
	video.load();
	audio.pause();
	audio.removeAttribute('src');
	audio.load();
	currentPreviewUrl = String(src || '');
	if (currentPreviewType === 'img') {
		img.src = currentPreviewUrl;
		return;
	}
	if (currentPreviewType === 'video') {
		video.src = currentPreviewUrl;
		return;
	}
	if (currentPreviewType === 'audio') {
		audio.src = currentPreviewUrl;
	}
};

// 全局点击事件
document.addEventListener('click', (event) => {
	const node = event.target;

	// 媒体预览器
	if (node.classList.contains('img-node')) {
		openPreview(node.src, 'img');
	}
});

const onResize = () => {
	if (!previewBox || !img) return;
	const x = (window.innerWidth - img.offsetWidth) / 2;
	const y = (window.innerHeight - img.offsetHeight) / 2;

	img.style.transition = `transform 0.4s ease, top 0.4s ease, left 0.4s ease, opacity 0.4s ease`;

	img.style.left = `${x}px`;
	img.style.top = `${y}px`;
};

if (previewBox && img) {
	img.addEventListener('load', () => {
		// 图片居中
		onResize();
		// 初始大小稍小于屏幕最大尺寸
		img.style.transform = `scale(0.85)`;
		previewBox.classList.remove('closing');
	});
	window.addEventListener('resize', onResize);

	previewBox.addEventListener('mousedown', (event) => {
		if (currentPreviewType !== 'img' && event.target !== previewBox) {
			return;
		}
		// 要求左键点击
		if(event.button !== 0) return;

		let moveX = event.clientX;
		let moveY = event.clientY;

		const onMouseUp = () => {
			if(moveX === event.clientX && moveY === event.clientY){
				closePreview();
			}
			previewBox.removeEventListener('mouseup', onMouseUp);
			previewBox.removeEventListener('mousemove', onMouseMove);
		};

		const onMouseMove = (event) => {
			moveX = event.clientX;
			moveY = event.clientY;
		};

		previewBox.addEventListener('mouseup', onMouseUp);
		previewBox.addEventListener('mousemove', onMouseMove);
	});
}

// 如果元素 (即将) 超出视口, 则重置位置
const runAway = async () => {
	if (!previewBox || !img) return;
	const rect = img.getBoundingClientRect();
	const stat =	rect.left > (window.innerWidth - window.innerWidth * 0.1) ||
					rect.right < window.innerWidth * 0.1 ||
					rect.top > (window.innerHeight - window.innerHeight * 0.1) ||
					rect.bottom < window.innerHeight * 0.1;
	if(stat){
		onResize();
	}
};

if (previewBox) {
	previewBox.addEventListener('wheel', (event) => {
		if (!previewBox || !img || currentPreviewType !== 'img') return;
		if (previewBox.classList.contains('open')) {
			// Lock scroll chain: do not allow the underlying container/page to scroll.
			event.preventDefault();
		}

		// (Abs(滚轮步进距离 / 1000), 限制不小于 0.01, 不大于 0.2. 乘 Abs(当前缩放比例)), 不小于 0.01
		const step = Math.max(Math.min(Math.max(Math.abs(event.deltaY / 1000), 0.01), 0.2) * Math.abs(scale), 0.01);

		if(step === 0.01){
			// 可能是通过触摸板进行缩放, 调低过度动画时间
			img.style.transition = `transform 0.25s ease, top 0.4s ease, left 0.4s ease, opacity 0.4s ease`;
		}else{
			img.style.transition = `transform 0.4s ease, top 0.4s ease, left 0.4s ease, opacity 0.4s ease`;
		}

		scale += (event.deltaY < 0)? step : -step;

		// 小数位数太多造成抖动
		img.style.transform = `scale(${scale.toFixed(4)})`;

		runAway();
	}, { passive: false });
}

if (previewBox && img) {
	img.addEventListener('mousedown', (event) => {
		if (!previewBox || !img || currentPreviewType !== 'img') return;
		const startMouseX = event.clientX;
		const startMouseY = event.clientY;
		const startX = img.offsetLeft;
		const startY = img.offsetTop;

		const onMouseMove = (event) => {
			// 拖拽需要跟手，不要过渡动画
			img.style.transition = 'none';

			const dx = event.clientX - startMouseX;
			const dy = event.clientY - startMouseY;
			img.style.left = `${startX + dx}px`;
			img.style.top = `${startY + dy}px`;
		};

		const onMouseUp = () => {
			previewBox.removeEventListener('mousemove', onMouseMove);
			previewBox.removeEventListener('mouseup', onMouseUp);

			runAway();
		};

		previewBox.addEventListener('mousemove', onMouseMove);
		previewBox.addEventListener('mouseup', onMouseUp);
	});
}
