package main

import (
	"io"

	"IpacPanel/daemon/ipc"
)

const maxIPCHeaderSize = ipc.MaxHeaderSize
const maxIPCBodySize = ipc.MaxBodySize
const ipcRequestInputStdin = ipc.RequestInputStdin

const (
	ControllerShutdownPurposeRestart = ipc.ControllerShutdownPurposeRestart
	ControllerShutdownPurposeUpdate  = ipc.ControllerShutdownPurposeUpdate
)

type IPCRequest = ipc.Request
type IPCResponse = ipc.Response

func NewIPCResponse(typ string, instance string, data []byte, errMsg string) IPCResponse {
	return ipc.NewResponse(typ, instance, data, errMsg)
}

type IPCConn = ipc.Conn

func NewIPCConn(reader io.Reader, writer io.Writer, closer io.Closer) *IPCConn {
	return ipc.NewConn(reader, writer, closer)
}

type InstanceRuntimeState = ipc.InstanceRuntimeState

const (
	InstanceLifecycleStopped  = ipc.InstanceLifecycleStopped
	InstanceLifecycleRunning  = ipc.InstanceLifecycleRunning
	InstanceLifecycleStopping = ipc.InstanceLifecycleStopping
	InstanceLifecycleCleaning = ipc.InstanceLifecycleCleaning
)

const (
	NoTerminal  = ipc.NoTerminal
	Terminal    = ipc.Terminal
	PTYTerminal = ipc.PTYTerminal
)

func NormalizeTerminalMode(mode int) int {
	return ipc.NormalizeTerminalMode(mode)
}

func IsNoTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == NoTerminal
}

func IsPTYTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == PTYTerminal
}

const (
	RuntimeCodeUnknown        = ipc.RuntimeCodeUnknown
	RuntimeCodeRunning        = ipc.RuntimeCodeRunning
	RuntimeCodeManualStop     = ipc.RuntimeCodeManualStop
	RuntimeCodeManualKill     = ipc.RuntimeCodeManualKill
	RuntimeCodeRestarting     = ipc.RuntimeCodeRestarting
	RuntimeCodeUnexpectedExit = ipc.RuntimeCodeUnexpectedExit
)
