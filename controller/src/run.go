package backend

import (
	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/metrics"
	"IpacPanel/controller/src/msg"
	"IpacPanel/daemon/version"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"
	web "IpacPanel/controller/src/web"
	api "IpacPanel/controller/src/web/api"
)

var versionedPublicPathPattern = regexp.MustCompile(`^/v\d+(?:\.\d+)*(?:/.*)?$`)

func isExpectedControllerServerCloseError(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed)
}

func waitControllerServerExit(serverErrCh <-chan error) error {
	select {
	case err := <-serverErrCh:
		if isExpectedControllerServerCloseError(err) {
			return nil
		}
		return err
	case <-time.After(2 * time.Second):
		log.Print(msg.WaitControllerServerExitTimeoutAfterShutdownLog)
		return nil
	}
}

func stripVersionedPublicPath(requestPath string) (string, bool) {
	if !versionedPublicPathPattern.MatchString(requestPath) {
		return requestPath, false
	}

	prefixEnd := strings.Index(requestPath[1:], "/")
	if prefixEnd == -1 {
		return "", true
	}

	prefixEnd++
	publicPath := requestPath[prefixEnd:]
	if publicPath == "" {
		return "/", true
	}
	return publicPath, true
}

func resolvePublicAssetAlias(publicPath string) string {
	const monacoNlsPrefix = "/vs/nls.messages."
	if strings.HasPrefix(publicPath, monacoNlsPrefix) && strings.HasSuffix(publicPath, ".js") && !strings.Contains(publicPath[len(monacoNlsPrefix):], "/") {
		return "/lib/monaco-editor/" + strings.TrimPrefix(publicPath, "/vs/") + ".js"
	}
	return publicPath
}

type RunOptions struct {
	AutoStartInstances bool
	DaemonStdio        bool
}

type fallbackPublicFS struct {
	primary  http.FileSystem
	fallback http.FileSystem
}

func (f fallbackPublicFS) Open(name string) (http.File, error) {
	file, err := f.primary.Open(name)
	if err == nil {
		return file, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return f.fallback.Open(name)
	}
	return nil, err
}

func resolvePublicFS(embeddedPublicFS fs.FS) (http.FileSystem, string, error) {
	if _, err := fs.Stat(embeddedPublicFS, "index.html"); err != nil {
		return nil, "", fmt.Errorf(msg.EmbeddedPublicValidationFailedFmt, err)
	}

	embeddedHTTPFS := http.FS(embeddedPublicFS)
	publicDir := cfg.GetPublicDir()
	if cfg.PublicDirExists() {
		return fallbackPublicFS{primary: http.Dir(publicDir), fallback: embeddedHTTPFS}, publicDir + " [fallback: embedded:public]", nil
	}

	return embeddedHTTPFS, "embedded:public", nil
}

func createPublicHandler(publicFS http.FileSystem) http.Handler {
	fileServer := http.FileServer(publicFS)
	servePublicPath := func(w http.ResponseWriter, r *http.Request, publicPath string) {
		rewrittenRequest := new(http.Request)
		*rewrittenRequest = *r
		rewrittenURL := *r.URL
		rewrittenURL.Path = publicPath
		rewrittenURL.RawPath = ""
		rewrittenRequest.URL = &rewrittenURL
		fileServer.ServeHTTP(w, rewrittenRequest)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publicPath, versioned := stripVersionedPublicPath(r.URL.Path)
		if versioned {
			if publicPath == "" {
				http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
				return
			}
			servePublicPath(w, r, resolvePublicAssetAlias(publicPath))
			return
		}

		servePublicPath(w, r, resolvePublicAssetAlias(publicPath))
	})
}

func withForceHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if web.IsExternalHTTPS(r) {
			next.ServeHTTP(w, r)
			return
		}
		redirectURL := *r.URL
		redirectURL.Scheme = "https"
		redirectURL.Host = r.Host
		http.Redirect(w, r, redirectURL.String(), http.StatusPermanentRedirect)
	})
}

func loadWebTLSConfig(webConfig cfg.WebConfig) (*tls.Config, error) {
	certPath := cfg.ResolveWebCertificatePath(webConfig.PublicKeyPath)
	keyPath := cfg.ResolveWebCertificatePath(webConfig.PrivateKeyPath)
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf(msg.LoadHTTPSCertificateFailedFmt, certPath, keyPath, err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

func listenAndServeController(server *http.Server, webConfig cfg.WebConfig) error {
	if webConfig.EnableHTTPS {
		tlsConfig, err := loadWebTLSConfig(webConfig)
		if err != nil {
			return err
		}
		server.TLSConfig = tlsConfig
		return server.ListenAndServeTLS("", "")
	}
	return server.ListenAndServe()
}

func Run(embeddedPublicFS fs.FS, opts RunOptions) error {
	cfg.InitializeInstanceRegistryHook = process.InitializeInstanceRegistry
	process.SetResetUploadSessionsHook(api.ResetUploadSessions)
	process.SetInstanceStatusChangedHook(api.BroadcastInstanceStatusUpdate)
	if err := cfg.LoadConfig(); err != nil {
		return err
	}
	if !opts.DaemonStdio {
		return errors.New(msg.ControllerStdioIPCRequired)
	}
	if err := process.ConnectDaemonStdio(); err != nil {
		return err
	}
	defer process.DisconnectDaemon()
	daemonProtocol, err := process.DaemonHello()
	if err != nil {
		return err
	}
	if daemonProtocol != version.DaemonProtocol {
		return fmt.Errorf(msg.DaemonProtocolMismatchFmt, version.DaemonProtocol, daemonProtocol)
	}
	if err := process.SetDaemonDebug(cfg.GetDebug()); err != nil {
		return fmt.Errorf(msg.SyncDebugModeToDaemonFailedFmt, err)
	}
	runtimeStates, err := process.ListDaemonRuntime()
	if err != nil {
		return err
	}
	process.RestoreDaemonRuntimeStates(runtimeStates)
	file.SetRegistryPath(cfg.ResolveDataPath("temp.yml"))
	if err := file.CleanupRegisteredAtomicTemps(); err != nil {
		log.Printf("cleanup registered atomic temps failed: %v", err)
	}
	api.CleanupUploadTempDir()
	process.InitTaskScheduler()
	defer process.DisconnectAllInstanceClients()
	defer process.StopTaskScheduler()
	defer api.StopInstanceStatusTicker()
	metricsConfig := cfg.GetMetricsConfig()
	dashboardCollector := metrics.NewCollector(metrics.Config{
		Enabled:               metricsConfig.Enabled,
		StorageMode:           metricsConfig.StorageMode,
		MemoryMaxMin:          metricsConfig.MemoryMaxMin,
		SQLiteMaxDay:          metricsConfig.SQLiteMaxDay,
		SQLiteCompactAfterDay: metricsConfig.SQLiteCompactAfterDay,
		SQLitePath:            cfg.ResolveDataPath("dashboard") + string(os.PathSeparator),
	})
	api.SetDashboardCollector(dashboardCollector)
	defer dashboardCollector.Stop()
	shutdownRequested := make(chan struct{})
	api.SetControllerShutdownHook(func() {
		select {
		case <-shutdownRequested:
		default:
			close(shutdownRequested)
		}
	})
	defer api.SetControllerShutdownHook(nil)
	process.RebuildAllInstanceTasksLocked()
	mux := http.NewServeMux()
	publicFS, publicSource, err := resolvePublicFS(embeddedPublicFS)
	if err != nil {
		return err
	}

	api.RegisterRoutes(mux)

	// Public
	mux.Handle("/", createPublicHandler(publicFS))

	addr := cfg.GetListenAddress()
	webConfig := cfg.GetWebConfig()
	scheme := "http"
	if webConfig.EnableHTTPS {
		scheme = "https"
	}
	log.Printf("== IpacPanel ==")
	log.Printf(msg.PublicAssetSourceFmt, publicSource)
	log.Printf(msg.PanelStartedFmt, scheme, addr)

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	handler := web.WithRequestLogging(web.WithRecover(web.WithResponseCompression(web.WithMaxRequestBody(mux))))
	if webConfig.ForceHTTPS {
		handler = withForceHTTPS(handler)
	}
	serverBaseCtx, stopServerBaseCtx := context.WithCancel(context.Background())
	defer stopServerBaseCtx()
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: 60 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return serverBaseCtx
		},
	}
	serverErrCh := make(chan error, 1)
	go func() {
		err := listenAndServeController(server, webConfig)
		if !isExpectedControllerServerCloseError(err) {
			serverErrCh <- err
			return
		}
		serverErrCh <- nil
	}()

	// Fail fast when startup fails (e.g. port already in use).
	select {
	case err := <-serverErrCh:
		return err
	case <-time.After(200 * time.Millisecond):
	}
	if err := process.NotifyControllerReady(); err != nil {
		return fmt.Errorf(msg.NotifyDaemonControllerReadyFailedFmt, err)
	}

	process.RestoreDaemonAutoRestarts(runtimeStates)

	if !opts.AutoStartInstances {
		log.Println(msg.ControllerRestartedAutoStartSkipped)
	} else {
		log.Println(msg.AutoStartInstancesHeader)
		for _, sp := range process.GetAutoStartProcesses() {
			select {
			case err := <-serverErrCh:
				return err
			default:
			}
			ins := sp.InstanceSnapshot()
			priorityText := ""
			if ins.StartPriority == nil {
				priorityText = msg.AutoStartPriorityEmpty
			} else {
				priorityText = strconv.Itoa(*ins.StartPriority)
			}
			log.Printf(msg.AutoStartInstanceLogFmt, priorityText, ins.Name)
			if err := sp.Start(); err != nil {
				limit := cfg.GetHistoryLimit() * 1024
				sp.Mu.Lock()
				sp.AppendAndBroadcastWarningSystemMessageLocked(fmt.Sprintf("%s: %s", msg.StartInstanceFailed, err.Error()), limit)
				sp.Mu.Unlock()
			}
			interval := cfg.GetAutoStartInterval()
			if interval > 0 {
				time.Sleep(time.Duration(interval) * time.Millisecond)
			}
		}
		log.Println(msg.AutoStartCompletedHeader)
	}

	select {
	case <-shutdownRequested:
		stopServerBaseCtx()
		process.DisconnectAllInstanceClients()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(ctx)
		cancel()
		if errors.Is(shutdownErr, context.DeadlineExceeded) {
			log.Printf(msg.ControllerShutdownTimeoutForceCloseLogFmt, shutdownErr)
			closeErr := server.Close()
			if !isExpectedControllerServerCloseError(closeErr) {
				shutdownErr = fmt.Errorf(msg.ForceCloseControllerServerAfterShutdownTimeoutFailedFmt, closeErr)
			} else {
				shutdownErr = nil
			}
		}
		serverErr := waitControllerServerExit(serverErrCh)
		api.CleanupUploadTempDir()
		if err := file.CleanupRegisteredAtomicTemps(); err != nil {
			return err
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		return serverErr
	case serverErr := <-serverErrCh:
		api.CleanupUploadTempDir()
		if err := file.CleanupRegisteredAtomicTemps(); err != nil {
			return err
		}
		return serverErr
	}
}
