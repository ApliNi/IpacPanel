package main

import (
	backend "IpacPanel/controller/src"
	"IpacPanel/daemon/version"
	"bytes"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

type versionInfo struct {
	Role           string `yaml:"role"`
	Version        string `yaml:"version"`
	DaemonProtocol int    `yaml:"daemon_protocol"`
}

//go:embed public/**
var embeddedPublicFS embed.FS

func publicFS() (fs.FS, error) {
	return fs.Sub(embeddedPublicFS, "public")
}

func PrintVersion() {
	info := versionInfo{
		Role:           "controller",
		Version:        version.ControllerVersion,
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

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	daemonStdio := flag.Bool("daemon-stdio", false, "use stdin/stdout daemon IPC")
	daemonFirstStart := flag.Bool("daemon-first-start", false, "whether this controller is the first daemon-managed start")
	flag.Parse()

	if *versionFlag {
		PrintVersion()
		return
	}
	initProcessLogger("C")

	embeddedFS, err := publicFS()
	if err != nil {
		log.Fatal(err)
	}

	if !*daemonStdio {
		fmt.Fprintln(os.Stderr, "controller must be started by daemon")
		os.Exit(2)
	}

	if err := backend.Run(embeddedFS, backend.RunOptions{AutoStartInstances: *daemonFirstStart, DaemonStdio: *daemonStdio}); err != nil {
		log.Fatal(err)
	}
}
