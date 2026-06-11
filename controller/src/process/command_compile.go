package process

import (
	"IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kballard/go-shellquote"
)

func CompileInstanceCommandArgv(command string, path string, terminal int) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		if config.IsNoTerminal(terminal) {
			return nil, errors.New(msg.NoTerminalCommandRequired)
		}
		return defaultShellArgv(), nil
	}
	return compileCommandStringArgv(command, path)
}

func CompileInstanceCleanupCommandArgv(command string, path string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, nil
	}
	return compileCommandStringArgv(command, path)
}

func compileCommandStringArgv(command string, path string) ([]string, error) {
	argv, err := shellquote.Split(command)
	if err != nil {
		return nil, fmt.Errorf(msg.ParseCommandFailedFmt, err)
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return nil, errors.New(msg.CommandEmpty)
	}
	return compileScriptCommandArgv(argv, path)
}

func defaultShellArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe"}
	}
	return []string{"sh"}
}

func compileScriptCommandArgv(argv []string, path string) ([]string, error) {
	args := append([]string(nil), argv...)
	script := resolveScriptPath(path, args[0])
	ext := strings.ToLower(filepath.Ext(script.statPath))
	switch runtime.GOOS {
	case "windows":
		switch ext {
		case ".bat", ".cmd":
			args[0] = script.runPath
			if err := validateWindowsBatchCommandArgs(args); err != nil {
				return nil, err
			}
			return []string{"cmd.exe", "/d", "/s", "/c", windowsCommandLine(args)}, nil
		case ".ps1":
			args[0] = script.runPath
			return append([]string{"powershell.exe", "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}, args...), nil
		}
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		if ext == ".sh" && isExecutableScript(script.statPath) {
			args[0] = unixExecutableScriptArg(script.runPath)
			return args, nil
		}
		if ext == ".sh" {
			return append([]string{"sh"}, args...), nil
		}
	}
	return args, nil
}

func validateWindowsBatchCommandArgs(args []string) error {
	for _, arg := range args {
		if strings.ContainsAny(arg, "&|<>()^%!\r\n") {
			return fmt.Errorf(msg.WindowsBatchCommandArgUnsafeFmt, arg)
		}
	}
	return nil
}

type scriptPathResolution struct {
	statPath string
	runPath  string
}

func resolveScriptPath(path string, script string) scriptPathResolution {
	resolution := scriptPathResolution{statPath: script, runPath: script}
	if filepath.IsAbs(script) || path == "" {
		return resolution
	}
	candidate := filepath.Join(path, script)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return resolution
	}
	resolution.statPath = candidate
	if runtime.GOOS == "windows" {
		resolution.runPath = candidate
	}
	return resolution
}

func isExecutableScript(statPath string) bool {
	info, err := os.Stat(statPath)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func unixExecutableScriptArg(script string) string {
	if filepath.IsAbs(script) || filepath.Base(script) != script {
		return script
	}
	return "." + string(filepath.Separator) + script
}

func windowsCommandLine(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, windowsEscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func windowsEscapeArg(arg string) string {
	if arg == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(arg, " \t\n\v\"")
	if !needsQuote {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		if r == '\\' {
			backslashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
			continue
		}
		if backslashes > 0 {
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
		}
		b.WriteRune(r)
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
