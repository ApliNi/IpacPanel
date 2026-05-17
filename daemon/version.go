package main

import (
	"IpacPanel/daemon/version"
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	Version = "0.1.0"
)

type versionInfo struct {
	Role           string `yaml:"role"`
	Version        string `yaml:"version"`
	DaemonProtocol int    `yaml:"daemon_protocol"`
}

func PrintVersion() {
	info := versionInfo{
		Role:           "daemon",
		Version:        Version,
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
