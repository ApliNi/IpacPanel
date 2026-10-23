package web

import (
	"IpacPanel/controller/src/web/authz"
	"net/http"
	"sync"
)

const (
	maxUserSSEStreams   = 128
	maxGlobalSSEStreams = 64
)

var (
	sseGlobalLimiter = make(chan struct{}, maxGlobalSSEStreams)
	sseUserStreamsMu sync.Mutex
	sseUserStreams   = make(map[string]int)
)

// AcquireSSESlot 为 SSE 连接申请并发流额度: 已登录用户使用按用户的额度,
// 未登录 (如公共仪表板) 使用全局额度. 返回的 release 用于归还额度.
func AcquireSSESlot(r *http.Request) (release func(), ok bool) {
	username, authenticated := authz.UsernameFromRequest(r)
	if authenticated && username != "" {
		sseUserStreamsMu.Lock()
		if sseUserStreams[username] >= maxUserSSEStreams {
			sseUserStreamsMu.Unlock()
			return nil, false
		}
		sseUserStreams[username]++
		sseUserStreamsMu.Unlock()

		var once sync.Once
		return func() {
			once.Do(func() {
				sseUserStreamsMu.Lock()
				if sseUserStreams[username] <= 1 {
					delete(sseUserStreams, username)
				} else {
					sseUserStreams[username]--
				}
				sseUserStreamsMu.Unlock()
			})
		}, true
	}
	select {
	case sseGlobalLimiter <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-sseGlobalLimiter })
		}, true
	default:
		return nil, false
	}
}
