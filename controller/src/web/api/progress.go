package api

import (
	"errors"
	"time"

	"IpacPanel/controller/src/web/authz"
)

const sseProgressThrottleInterval = 150 * time.Millisecond

// errUserStreamInactive 表示流式任务进行期间用户已被禁用或删除, 需要中止任务.
var errUserStreamInactive = errors.New("user stream inactive")

// userStreamActive 报告用户名对应的用户当前是否仍有效 (未被禁用或删除).
func userStreamActive(username string) bool {
	_, ok := authz.AuthUserByUsername(username)
	return ok
}
