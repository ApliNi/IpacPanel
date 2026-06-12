package compat

import "IpacPanel/daemon/terminal"

const DefaultTerminalEncoding = terminal.DefaultTerminalEncoding

func NormalizeTerminalEncoding(name string) (string, bool) {
	return terminal.NormalizeTerminalEncoding(name)
}
