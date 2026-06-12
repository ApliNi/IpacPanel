import { parseSSEJsonData, readSSEStream } from '../api/core.js';

const DEFAULT_SSE_EVENT_TYPES = ['message', 'progress', 'fail', 'error', 'end', 'done'];

const decodeSSEEventPayload = (event, parseErrorMessage) => {
	try {
		return parseSSEJsonData(event, parseErrorMessage);
	} catch (error) {
		return {
			raw: String(event.data || '').trim(),
			parse_error: error instanceof Error ? String(error.message || '') : String(error || ''),
		};
	}
};

export const readJsonSSEStream = async (res, options = {}) => {
	const onEvent = options.onEvent;
	if (typeof onEvent !== 'function') {
		throw new Error('SSE JSON 事件处理函数不可用');
	}
	const parseErrorMessage = String(options.parseErrorMessage || 'SSE 事件解析失败');
	const eventTypes = Array.isArray(options.eventTypes) && options.eventTypes.length
		? options.eventTypes
		: DEFAULT_SSE_EVENT_TYPES;
	const handlers = {};
	for (const eventType of eventTypes) {
		const name = String(eventType || '').trim();
		if (!name) {
			continue;
		}
		handlers[name] = (event) => {
			const payload = decodeSSEEventPayload(event, parseErrorMessage);
			onEvent(String(event.type || 'message'), payload);
		};
	}
	await readSSEStream(res, handlers);
};
