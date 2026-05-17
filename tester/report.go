package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type reporter struct {
	path string
	mu   sync.Mutex
}

func newReporter(path string) *reporter { return &reporter{path: path} }

func (r *reporter) beginRun() error {
	return r.append(fmt.Sprintf("\n## 测试结果\n\n- 开始时间: %s\n\n", time.Now().Format(time.RFC3339)))
}

func (r *reporter) beginSuite(name string) error {
	return r.append(fmt.Sprintf("### %s\n\n", name))
}

func (r *reporter) add(result testResult) error {
	mark := " "
	if result.Passed {
		mark = "x"
	}
	text := fmt.Sprintf("- [%s] %s\n", mark, result.Name)
	if result.Detail != "" {
		text += "  - 说明: " + result.Detail + "\n"
	}
	if result.Duration > 0 {
		text += "  - 耗时: " + result.Duration.String() + "\n"
	}
	for _, evidence := range result.Evidence {
		if evidence != "" {
			text += "  - 证据: " + evidence + "\n"
		}
	}
	return r.append(text)
}

func (r *reporter) append(text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func (ctx *suiteContext) record(name string, started time.Time, err error, evidence ...string) {
	result := testResult{Suite: ctx.Name, Name: name, Passed: err == nil, Duration: time.Since(started), Evidence: evidence}
	if err != nil {
		result.Detail = err.Error()
	}
	if writeErr := ctx.Env.Reporter.add(result); writeErr != nil {
		fmt.Fprintf(os.Stderr, "写入测试报告失败: %v\n", writeErr)
	}
}
