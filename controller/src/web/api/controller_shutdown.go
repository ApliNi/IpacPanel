package api

import "sync"

var controllerShutdownHook struct {
	mu sync.Mutex
	fn func()
}

func SetControllerShutdownHook(fn func()) {
	controllerShutdownHook.mu.Lock()
	defer controllerShutdownHook.mu.Unlock()
	controllerShutdownHook.fn = fn
}

func requestControllerShutdown() {
	controllerShutdownHook.mu.Lock()
	fn := controllerShutdownHook.fn
	controllerShutdownHook.mu.Unlock()
	if fn != nil {
		fn()
	}
}
