package main

import (
	"IpacPanel/daemon/version"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

var (
	appBaseDir  string
	instanceMgr *InstanceManager
	ipcDebug    atomic.Bool
)

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

	log.Printf("IpacPanel Daemon v%s [protocol=%d]", Version, version.DaemonProtocol)
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

	if err := controllerLoop(controllerPath, server); err != nil {
		fmt.Fprintf(os.Stderr, "controller loop: %v\n", err)
		os.Exit(1)
	}
}
