package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type suiteRunner struct {
	key  string
	name string
	fn   func(*testEnv) error
}

var suites = []suiteRunner{
	{key: "init", name: "初始化", fn: runInitSuite},
	{key: "autostart", name: "启动实例", fn: runAutoStartSuite},
	{key: "restart", name: "自动重启", fn: runRestartSuite},
	{key: "tasks", name: "计划任务", fn: runTasksSuite},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "测试器错误: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	runTest := flag.Bool("run-test", false, "启动测试流程")
	helper := flag.Bool("helper", false, "以辅助实例模式运行")
	suite := flag.String("suite", "all", "测试套件: all/init/autostart/restart/tasks")
	helperID := flag.String("helper-id", "", "辅助实例 ID")
	helperCase := flag.String("case", "long-running", "辅助实例行为")
	eventDir := flag.String("event-dir", "", "事件输出目录")
	heartbeat := flag.Duration("heartbeat", time.Second, "心跳间隔")
	exitAfter := flag.Duration("exit-after", 0, "启动后退出延迟")
	exitCode := flag.Int("exit-code", 0, "退出码")
	expectCommand := flag.String("expect-command", "", "期望命令")
	stopOnCommand := flag.Bool("stop-on-command", false, "收到命令后退出")
	memoryMB := flag.Int("memory-mb", 0, "辅助实例保留内存 MB")
	cpuLoad := flag.Int("cpu-load", 0, "辅助实例 CPU 压力百分比")
	flag.Parse()

	if *helper {
		return runHelper(helperOptions{ID: *helperID, Case: *helperCase, EventDir: *eventDir, Heartbeat: *heartbeat, ExitAfter: *exitAfter, ExitCode: *exitCode, ExpectCommand: *expectCommand, StopOnCommand: *stopOnCommand, MemoryMB: *memoryMB, CPULoad: *cpuLoad})
	}
	if *runTest {
		return runTests(*suite)
	}
	return printHelp()
}

func printHelp() error {
	fmt.Println("## IpacPanel 测试工具")
	fmt.Println()
	fmt.Println("1. 将测试器和面板软件放置在同一目录")
	fmt.Println("2. 使用 `--run-test` 启动测试")
	fmt.Println()
	fmt.Println("测试结果将生成在 `./tester.md` 文件.")
	return nil
}

func runTests(suite string) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取当前目录: %w", err)
	}
	testerExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取测试器路径: %w", err)
	}
	distributionDir, err := locateDistributionDir(root, filepath.Dir(testerExe))
	if err != nil {
		return err
	}
	if !validSuite(suite) {
		return fmt.Errorf("未知测试套件: %s", suite)
	}
	report := newReporter(filepath.Join(distributionDir, testerReportName))
	if err := report.beginRun(); err != nil {
		return err
	}
	env := &testEnv{RootDir: root, DistributionDir: distributionDir, RunDir: filepath.Join(distributionDir, testerWorkDirName, time.Now().Format("20060102-150405")), TesterExe: testerExe, Reporter: report}
	if err := os.MkdirAll(env.RunDir, 0755); err != nil {
		return fmt.Errorf("创建测试工作目录: %w", err)
	}
	for _, runner := range suites {
		if suite != "all" && suite != runner.key {
			continue
		}
		if err := report.beginSuite(runner.name); err != nil {
			return err
		}
		if err := runner.fn(env); err != nil {
			fmt.Fprintf(os.Stderr, "套件 %s 失败: %v\n", runner.key, err)
		}
	}
	return nil
}

func validSuite(suite string) bool {
	if suite == "all" {
		return true
	}
	for _, runner := range suites {
		if runner.key == suite {
			return true
		}
	}
	return false
}
