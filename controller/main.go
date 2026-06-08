package main

import (
	backend "IpacPanel/controller/src"
	"IpacPanel/controller/src/msg"
	"IpacPanel/daemon/version"
	"bytes"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	_ "time/tzdata"

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
		Version:        version.Version,
		DaemonProtocol: version.DaemonProtocol,
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]versionInfo{"version": info}); err != nil {
		fmt.Fprintf(os.Stderr, msg.ControllerVersionMarshalFailedFmt, err)
		os.Exit(1)
	}
	if err := enc.Close(); err != nil {
		fmt.Fprintf(os.Stderr, msg.ControllerVersionEncoderCloseFailedFmt, err)
		os.Exit(1)
	}
	fmt.Print(out.String())
}

func main() {
	versionFlag := flag.Bool("version", false, msg.ControllerVersionFlagHelp)
	daemonStdio := flag.Bool("daemon-stdio", false, msg.ControllerDaemonStdioFlagHelp)
	daemonFirstStart := flag.Bool("daemon-first-start", false, msg.ControllerDaemonFirstStartFlagHelp)
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
		fmt.Fprintln(os.Stderr, msg.ControllerMustStartByDaemon)
		os.Exit(2)
	}

	if err := backend.Run(embeddedFS, backend.RunOptions{AutoStartInstances: *daemonFirstStart, DaemonStdio: *daemonStdio}); err != nil {
		log.Fatal(err)
	}
}
