package main

import (
	"IpacPanel/daemon/version"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const outputChannelSize = 256

const (
	ipcControlQueueSize       = 1024
	ipcOutputFlushInterval    = 8 * time.Millisecond
	ipcOutputMaxBatchBytes    = 512 * 1024
	ipcOutputDrainMaxMessages = 128
)

type IPCServer struct {
	Conn            *IPCConn
	Instances       *InstanceManager
	outputCh        chan IPCResponse
	done            chan struct{}
	closeOnce       sync.Once
	serveDone       chan struct{}
	readyCh         chan struct{}
	connMu          sync.Mutex
	inputErrMu      sync.Mutex
	lastInputErrLog time.Time

	controllerUpdateRestartPending atomic.Bool
}

func NewIPCServer(instances *InstanceManager) (*IPCServer, error) {
	return &IPCServer{
		Instances: instances,
		outputCh:  make(chan IPCResponse, outputChannelSize),
		done:      make(chan struct{}),
	}, nil
}

func (s *IPCServer) OutputCh() chan<- IPCResponse {
	return s.outputCh
}

func (s *IPCServer) Bind(conn *IPCConn) {
	s.connMu.Lock()
	oldConn := s.Conn
	oldServeDone := s.serveDone
	s.connMu.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}
	if oldServeDone != nil {
		select {
		case <-oldServeDone:
		case <-time.After(5 * time.Second):
			log.Printf("wait old IPC serve done timeout")
		}
	}
	s.drainOutput()
	s.connMu.Lock()
	s.Conn = conn
	s.serveDone = make(chan struct{})
	s.connMu.Unlock()
}

func (s *IPCServer) drainOutput() {
	for {
		select {
		case resp := <-s.outputCh:
			resp.Release()
		default:
			return
		}
	}
}

func (s *IPCServer) Serve(readyCh chan struct{}) {
	connDone := make(chan struct{})
	forwardDone := make(chan struct{})
	s.connMu.Lock()
	s.readyCh = readyCh
	conn := s.Conn
	serveDone := s.serveDone
	s.connMu.Unlock()
	if conn == nil {
		return
	}
	controlCh := make(chan IPCResponse, ipcControlQueueSize)
	defer func() {
		close(connDone)
		<-forwardDone
		s.connMu.Lock()
		if s.Conn == conn {
			_ = s.Conn.Close()
			s.Conn = nil
		}
		if s.serveDone == serveDone && s.serveDone != nil {
			close(serveDone)
			s.serveDone = nil
		}
		if s.readyCh == readyCh {
			s.readyCh = nil
		}
		s.connMu.Unlock()
	}()

	go func() {
		defer close(forwardDone)
		s.forwardOutput(conn, controlCh, connDone)
	}()

	for {
		req, err := conn.ReadRequest()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			log.Printf("IPC read error: %v", err)
			return
		}

		resp := s.dispatch(req)
		if resp != nil {
			resp.ID = req.ID
			if !s.queueControlResponse(controlCh, forwardDone, *resp) {
				return
			}
		}
	}
}

func (s *IPCServer) queueControlResponse(controlCh chan<- IPCResponse, forwardDone <-chan struct{}, resp IPCResponse) bool {
	select {
	case controlCh <- resp:
		return true
	case <-forwardDone:
		resp.Release()
		return false
	case <-s.done:
		resp.Release()
		return false
	}
}

func (s *IPCServer) forwardOutput(conn *IPCConn, controlCh <-chan IPCResponse, connDone <-chan struct{}) {
	ticker := time.NewTicker(ipcOutputFlushInterval)
	defer ticker.Stop()
	pending := make(map[string]IPCResponse)
	pendingBytes := 0
	releasePending := func() {
		for _, resp := range pending {
			resp.Release()
		}
		pending = make(map[string]IPCResponse)
		pendingBytes = 0
	}
	flushPending := func(conn *IPCConn) bool {
		if pendingBytes == 0 {
			return true
		}
		for instance, resp := range pending {
			delete(pending, instance)
			pendingBytes -= len(resp.Body)
			if err := conn.WriteInstanceOutputResponse(resp, false); err != nil {
				log.Printf("IPC output write error: %v", err)
				_ = conn.Close()
				releasePending()
				return false
			}
		}
		if err := conn.Flush(); err != nil {
			log.Printf("IPC output flush error: %v", err)
			_ = conn.Close()
			releasePending()
			return false
		}
		return true
	}
	queueOutput := func(resp IPCResponse) {
		if resp.Type != "o" || resp.Instance == "" || len(resp.Body) == 0 {
			resp.Release()
			return
		}
		current, ok := pending[resp.Instance]
		if ok {
			body := make([]byte, 0, len(current.Body)+len(resp.Body))
			body = append(body, current.Body...)
			body = append(body, resp.Body...)
			pendingBytes += len(resp.Body)
			current.Body = body
			current.release = mergeIPCRelease(current.release, resp.release)
			resp.release = nil
			pending[resp.Instance] = current
			return
		}
		pending[resp.Instance] = resp
		pendingBytes += len(resp.Body)
	}
	writeControl := func(conn *IPCConn, resp IPCResponse) bool {
		if err := conn.WriteResponse(resp); err != nil {
			log.Printf("IPC write error: %v", err)
			_ = conn.Close()
			return false
		}
		return true
	}
	for {
		for i := 0; i < ipcOutputDrainMaxMessages; i++ {
			select {
			case resp := <-controlCh:
				if !writeControl(conn, resp) {
					return
				}
				continue
			default:
			}
			break
		}

		select {
		case <-s.done:
			releasePending()
			return
		case <-connDone:
			releasePending()
			return
		case resp := <-controlCh:
			if !writeControl(conn, resp) {
				return
			}
		case resp := <-s.outputCh:
			if resp.Type != "o" {
				if !flushPending(conn) {
					resp.Release()
					return
				}
				if !writeControl(conn, resp) {
					return
				}
				continue
			}
			queueOutput(resp)
			if pendingBytes < ipcOutputMaxBatchBytes {
				continue
			}
			if !flushPending(conn) {
				return
			}
		case <-ticker.C:
			if !flushPending(conn) {
				return
			}
		}
	}
}

func mergeIPCRelease(first func(), second func()) func() {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func() {
		first()
		second()
	}
}

func (s *IPCServer) dispatch(req *IPCRequest) *IPCResponse {
	switch req.Type {
	case "start_instance":
		return s.handleStartInstance(req)
	case "hello":
		resp := IPCResponse{Type: "hello", Msg: req.Msg, DaemonProtocol: version.DaemonProtocol}
		return &resp
	case "list_runtime":
		resp := IPCResponse{Type: "runtime_snapshot", Runtime: s.Instances.ListRuntime()}
		return &resp
	case "set_debug":
		return s.handleSetDebug(req)
	case "restart_controller":
		return s.handleRestartController(req)
	case "controller_ready":
		return s.handleControllerReady(req)
	case "rename_daemon_instance":
		return s.handleRenameDaemonInstance(req)
	case "update_instance_config":
		return s.handleUpdateInstanceConfig(req)
	case "mark_runtime_code":
		return s.handleMarkRuntimeCode(req)
	case "mark_stopping":
		return s.handleMarkStopping(req)
	case "stop_instance":
		return s.handleStopInstance(req)
	case "kill_instance":
		return s.handleKillInstance(req)
	case ipcRequestInputStdin:
		return s.handleInputStdin(req)
	case "write_stdin":
		return s.handleWriteStdin(req)
	case "resize_terminal":
		return s.handleResizeTerminal(req)
	case "log":
		log.Printf("[controller] %s", string(req.Data))
		return nil
	default:
		resp := NewIPCResponse("instance_error", req.Instance, nil, "unknown request type: "+req.Type)
		return &resp
	}
}

func (s *IPCServer) handleControllerReady(req *IPCRequest) *IPCResponse {
	s.connMu.Lock()
	readyCh := s.readyCh
	s.connMu.Unlock()
	if readyCh != nil {
		select {
		case <-readyCh:
		default:
			close(readyCh)
		}
	}
	resp := NewIPCResponse("ok", "", nil, "")
	return &resp
}

func (s *IPCServer) handleSetDebug(req *IPCRequest) *IPCResponse {
	ipcDebug.Store(req.Debug)
	s.connMu.Lock()
	if s.Conn != nil {
		s.Conn.SetDebug(req.Debug)
	}
	s.connMu.Unlock()
	resp := NewIPCResponse("ok", "", nil, "")
	return &resp
}

func (s *IPCServer) handleRestartController(req *IPCRequest) *IPCResponse {
	purpose := req.ControllerShutdownPurpose
	if purpose == "" {
		purpose = ControllerShutdownPurposeRestart
	}
	switch purpose {
	case ControllerShutdownPurposeRestart:
	case ControllerShutdownPurposeUpdate:
		log.Printf("controller restart requested for update")
		s.controllerUpdateRestartPending.Store(true)
	default:
		resp := NewIPCResponse("controller_error", "", nil, "invalid controller shutdown purpose")
		return &resp
	}
	resp := NewIPCResponse("ok", "", nil, "")
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.connMu.Lock()
		conn := s.Conn
		s.connMu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	}()
	return &resp
}

func (s *IPCServer) ConsumeControllerUpdateRestartPending() bool {
	return s.controllerUpdateRestartPending.CompareAndSwap(true, false)
}

func (s *IPCServer) handleStartInstance(req *IPCRequest) *IPCResponse {
	if req.Instance == "" {
		resp := NewIPCResponse("instance_error", "", nil, "instance required")
		return &resp
	}
	if req.Command == "" {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "command required")
		return &resp
	}

	s.Instances.Mu.Lock()
	created := false
	if existing, ok := s.Instances.instances[req.Instance]; ok && existing != nil {
		existing.Mu.Lock()
		state := existing.State
		existing.Mu.Unlock()
		if state == instanceRunning {
			s.Instances.Mu.Unlock()
			runtime := existing.RuntimeSnapshot()
			resp := IPCResponse{Type: "instance_started", State: &runtime}
			return &resp
		}
		if state == instanceStopping {
			s.Instances.Mu.Unlock()
			resp := NewIPCResponse("instance_error", req.Instance, nil, daemonInstanceStopping)
			return &resp
		}
	} else {
		created = true
	}
	ins := s.Instances.prepareInstanceForStartLocked(req)
	s.Instances.instances[req.Instance] = ins
	s.Instances.Mu.Unlock()

	if err := ins.Start(s.outputCh); err != nil {
		if created {
			s.Instances.Delete(req.Instance)
		}
		resp := NewIPCResponse("instance_error", req.Instance, nil, err.Error())
		return &resp
	}
	state := ins.RuntimeSnapshot()
	resp := IPCResponse{Type: "instance_started", State: &state}
	return &resp
}

func (s *IPCServer) handleRenameDaemonInstance(req *IPCRequest) *IPCResponse {
	if req.Instance == "" || req.NewName == "" {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "old_name and new_name required")
		return &resp
	}
	if err := s.Instances.RenameInstance(req.Instance, req.NewName); err != nil {
		resp := NewIPCResponse("instance_error", req.Instance, nil, err.Error())
		return &resp
	}
	resp := NewIPCResponse("ok", req.NewName, nil, "")
	return &resp
}

func (s *IPCServer) handleUpdateInstanceConfig(req *IPCRequest) *IPCResponse {
	if req.Instance == "" {
		resp := NewIPCResponse("instance_error", "", nil, "instance required")
		return &resp
	}
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("ok", req.Instance, nil, "")
		return &resp
	}
	ins.UpdateRuntimeConfig(req.CleanupCommand)
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) handleStopInstance(req *IPCRequest) *IPCResponse {
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "instance not found")
		return &resp
	}
	ins.Stop(req.Force, req.RuntimeCode)
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) handleMarkRuntimeCode(req *IPCRequest) *IPCResponse {
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "instance not found")
		return &resp
	}
	ins.MarkRuntimeCode(req.RuntimeCode)
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) handleMarkStopping(req *IPCRequest) *IPCResponse {
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "instance not found")
		return &resp
	}
	ins.MarkStopping(req.RuntimeCode)
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) handleKillInstance(req *IPCRequest) *IPCResponse {
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "instance not found")
		return &resp
	}
	ins.Kill()
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) handleWriteStdin(req *IPCRequest) *IPCResponse {
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "instance not found")
		return &resp
	}
	if err := ins.WriteStdin(req.Data); err != nil {
		resp := NewIPCResponse("instance_error", req.Instance, nil, err.Error())
		return &resp
	}
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) handleInputStdin(req *IPCRequest) *IPCResponse {
	if req.BodyLen == 0 {
		return nil
	}
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		s.logInputStdinError(req.Instance, "instance not found")
		return nil
	}
	if err := ins.WriteStdin(req.Data); err != nil {
		s.logInputStdinError(req.Instance, err.Error())
		return nil
	}
	return nil
}

func (s *IPCServer) logInputStdinError(instance string, errMsg string) {
	const minInterval = time.Second
	now := time.Now()
	s.inputErrMu.Lock()
	defer s.inputErrMu.Unlock()
	if !s.lastInputErrLog.IsZero() && now.Sub(s.lastInputErrLog) < minInterval {
		return
	}
	s.lastInputErrLog = now
	log.Printf("write instance %s stdin failed: %s", instance, errMsg)
}

func (s *IPCServer) handleResizeTerminal(req *IPCRequest) *IPCResponse {
	ins, ok := s.Instances.Get(req.Instance)
	if !ok {
		resp := NewIPCResponse("instance_error", req.Instance, nil, "instance not found")
		return &resp
	}
	if err := ins.ResizeTerminal(req.Cols, req.Rows); err != nil {
		log.Printf("resize instance %s terminal failed: %v", req.Instance, err)
		resp := NewIPCResponse("instance_error", req.Instance, nil, err.Error())
		return &resp
	}
	resp := NewIPCResponse("ok", req.Instance, nil, "")
	return &resp
}

func (s *IPCServer) WaitServeDone(timeout time.Duration) {
	s.connMu.Lock()
	serveDone := s.serveDone
	s.connMu.Unlock()
	if serveDone == nil {
		return
	}
	if timeout <= 0 {
		<-serveDone
		return
	}
	select {
	case <-serveDone:
	case <-time.After(timeout):
	}
}

func (s *IPCServer) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.connMu.Lock()
		defer s.connMu.Unlock()
		if s.Conn != nil {
			_ = s.Conn.Close()
		}
	})
}
