package main

import (
	"IpacPanel/daemon/version"
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"

	"gopkg.in/yaml.v3"
)

var (
	appBaseDir  string
	instanceMgr *InstanceManager
	ipcDebug    atomic.Bool
)

type versionInfo struct {
	Role           string `yaml:"role"`
	Version        string `yaml:"version"`
	DaemonProtocol int    `yaml:"daemon_protocol"`
}

func PrintVersion() {
	info := versionInfo{
		Role:           "daemon",
		Version:        version.Version,
		DaemonProtocol: version.DaemonProtocol,
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]versionInfo{"version": info}); err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal version: %v\n", err)
		os.Exit(1)
	}
	if err := enc.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to close version encoder: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out.String())
}

func detectAppBaseDir() string {
	wd, err := os.Getwd()
	if err == nil {
		wd = strings.TrimSpace(wd)
		if wd != "" {
			wd = filepath.Clean(wd)
			if looksLikeAppBaseDir(wd) {
				return wd
			}
		}
	}

	exePath, err := os.Executable()
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
			exePath = resolved
		}
		dir := strings.TrimSpace(filepath.Dir(exePath))
		if dir != "" {
			dir = filepath.Clean(dir)
			if looksLikeAppBaseDir(dir) {
				return dir
			}
			return dir
		}
	}
	if wd != "" {
		return filepath.Clean(wd)
	}
	return "."
}

func looksLikeAppBaseDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if pathExists(filepath.Join(dir, "data")) {
		return true
	}
	if pathExists(filepath.Join(dir, "go.mod")) && pathExists(filepath.Join(dir, "controller", "src")) {
		return true
	}
	return false
}

func pathExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	debugFlag := flag.Bool("debug", false, "print daemon-controller stdio IPC frames to daemon logs")
	flag.Parse()
	ipcDebug.Store(*debugFlag)

	if *versionFlag {
		PrintVersion()
		return
	}

	appBaseDir = detectAppBaseDir()
	initProcessLogger("D")

	log.Printf("IpacPanel Daemon v%s [protocol=%d]", version.Version, version.DaemonProtocol)
	if runtime.GOOS == "windows" {
		log.Printf("platform: windows")
	} else {
		log.Printf("platform: %s", runtime.GOOS)
	}
	log.Printf("base directory: %s", appBaseDir)

	controllerPath, err := findControllerBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "find controller binary: %v\n", err)
		os.Exit(1)
	}
	log.Printf("controller binary: %s", controllerPath)

	instanceMgr = NewInstanceManager()

	server, err := NewIPCServer(instanceMgr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start IPC server: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	log.Printf("IPC server using controller stdio pipes")

	// 捕获退出信号, 清理所有实例进程
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down instances", sig)
		shutdownAllInstances()
		os.Exit(0)
	}()

	if err := controllerLoop(controllerPath, server); err != nil {
		log.Printf("controller loop error, shutting down instances: %v", err)
		shutdownAllInstances()
		os.Exit(1)
	}
}
