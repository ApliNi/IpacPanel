package web

import (
	"IpacPanel/controller/src/web/authz"
	"net/http"
	"sync"
)

var (
	sseGlobalLimiter  = make(chan struct{}, 64)
	sseUserLimitersMu sync.Mutex
	sseUserLimiters   = make(map[string]chan struct{})
)

// AcquireSSESlot 为 SSE 连接申请并发流额度: 已登录用户使用按用户的额度,
// 未登录 (如公共仪表板) 使用全局额度. 返回的 release 用于归还额度.
func AcquireSSESlot(r *http.Request) (release func(), ok bool) {
	username, authenticated := authz.UsernameFromRequest(r)
	if authenticated && username != "" {
		sseUserLimitersMu.Lock()
		ch, exists := sseUserLimiters[username]
		if !exists {
			ch = make(chan struct{}, 128)
			sseUserLimiters[username] = ch
		}
		sseUserLimitersMu.Unlock()
		select {
		case ch <- struct{}{}:
			return func() {
				<-ch
				sseUserLimitersMu.Lock()
				// 仅当 map 仍指向当前归还的通道且该通道已清空时才移除条目,
				// 避免并发 acquire 复用同一用户名新建的通道被误删.
				if current, exists := sseUserLimiters[username]; exists && current == ch && len(ch) == 0 {
					delete(sseUserLimiters, username)
				}
				sseUserLimitersMu.Unlock()
			}, true
		default:
			return nil, false
		}
	}
	select {
	case sseGlobalLimiter <- struct{}{}:
		return func() { <-sseGlobalLimiter }, true
	default:
		return nil, false
	}
}
