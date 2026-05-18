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

	"github.com/gorilla/websocket"
)

var versionedPublicPathPattern = regexp.MustCompile(`^/v\d+(?:\.\d+)*(?:/.*)?$`)

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
		return nil, fmt.Errorf("加载 HTTPS 证书失败, public=%s, private=%s: %w", certPath, keyPath, err)
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
		return fmt.Errorf("controller must be started with daemon stdio IPC")
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
		return fmt.Errorf("daemon protocol mismatch: controller=%d daemon=%d", version.DaemonProtocol, daemonProtocol)
	}
	if err := process.SetDaemonDebug(cfg.GetDebug()); err != nil {
		return fmt.Errorf("sync debug mode to daemon: %w", err)
	}
	runtimeStates, err := process.ListDaemonRuntime()
	if err != nil {
		return err
	}
	process.RestoreDaemonRuntimeStates(runtimeStates)
	file.SetRegistryPath(cfg.ResolveDataPath("temp.yml"))
	if err := file.CleanupRegisteredAtomicTempDirs(); err != nil {
		return err
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

	// Admin
	mux.HandleFunc("/api/admin/get", api.HandleApiAdminGet)
	mux.HandleFunc("/api/admin/create", api.HandleApiAdminCreate)
	mux.HandleFunc("/api/admin/update", api.HandleApiAdminUpdate)
	mux.HandleFunc("/api/admin/delete", api.HandleApiAdminDelete)

	// Group
	mux.HandleFunc("/api/group/update", api.HandleApiGroupUpdate)

	// Settings
	mux.HandleFunc("/api/settings/public", api.HandleApiSettingsPublic)
	mux.HandleFunc("/api/settings/get", api.HandleApiSettingsGet)
	mux.HandleFunc("/api/settings/update", api.HandleApiSettingsUpdate)
	mux.HandleFunc("/api/settings/restart-controller", api.HandleApiSettingsRestartController)

	// Dashboard
	mux.HandleFunc("/api/dashboard/events", api.HandleApiDashboardEvents)
	mux.HandleFunc("/api/dashboard/snapshot", api.HandleApiDashboardSnapshot)

	// Auth
	mux.HandleFunc("/api/auth/pow", api.HandleApiAuthPow)
	mux.HandleFunc("/api/auth/login", api.HandleApiAuthLogin)
	mux.HandleFunc("/api/auth/logout", api.HandleApiAuthLogout)
	mux.HandleFunc("/api/auth/reset", api.HandleApiAuthReset)

	// File
	mux.HandleFunc("/api/file/list", api.HandleApiFileList)
	mux.HandleFunc("/api/file/read", api.HandleApiFileRead)
	mux.HandleFunc("/api/file/raw", api.HandleApiFileRaw)
	mux.HandleFunc("/api/file/save", api.HandleApiFileSave)
	mux.HandleFunc("/api/file/create/file", api.HandleApiFileCreateFile)
	mux.HandleFunc("/api/file/create/dir", api.HandleApiFileCreateDir)
	mux.HandleFunc("/api/file/rename", api.HandleApiFileRename)
	mux.HandleFunc("/api/file/delete", api.HandleApiFileDelete)
	mux.HandleFunc("/api/file/upload/init", api.HandleApiFileUploadInit)
	mux.HandleFunc("/api/file/upload/chunk", api.HandleApiFileUploadChunk)
	mux.HandleFunc("/api/file/upload/abort", api.HandleApiFileUploadAbort)
	mux.HandleFunc("/api/file/upload/complete", api.HandleApiFileUploadComplete)
	mux.HandleFunc("/api/file/batch", api.HandleApiFileBatch)
	mux.HandleFunc("/api/file/extract", api.HandleApiFileExtract)

	// Controller update
	mux.HandleFunc("/api/controller/update/status", api.HandleApiControllerUpdateStatus)
	mux.HandleFunc("/api/controller/update/upload/init", api.HandleApiControllerUpdateUploadInit)
	mux.HandleFunc("/api/controller/update/upload/chunk", api.HandleApiControllerUpdateUploadChunk)
	mux.HandleFunc("/api/controller/update/upload/abort", api.HandleApiControllerUpdateUploadAbort)
	mux.HandleFunc("/api/controller/update/upload/complete", api.HandleApiControllerUpdateUploadComplete)
	mux.HandleFunc("/api/controller/update/apply", api.HandleApiControllerUpdateApply)

	// Instance
	mux.HandleFunc("/api/instance/events", api.HandleApiInstanceEvents)
	mux.HandleFunc("/api/instance/get", api.HandleApiInstanceGet)
	mux.HandleFunc("/api/instance/create", api.HandleApiInstanceCreate)
	mux.HandleFunc("/api/instance/update", api.HandleApiInstanceUpdate)
	mux.HandleFunc("/api/instance/delete", api.HandleApiInstanceDelete)
	mux.HandleFunc("/api/instance/control", api.HandleApiInstanceControl)
	mux.HandleFunc("/api/instance/ws", api.HandleApiInstanceWs)

	// User
	mux.HandleFunc("/api/user/get", api.HandleApiUserGet)
	mux.HandleFunc("/api/user/list", api.HandleApiUserList)
	mux.HandleFunc("/api/user/update", api.HandleApiUserUpdate)

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
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: 60 * time.Second,
		IdleTimeout:       120 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	serverErrCh := make(chan error, 1)
	go func() {
		err := listenAndServeController(server, webConfig)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		return fmt.Errorf("notify daemon controller ready: %w", err)
	}

	process.RestoreDaemonAutoRestarts(runtimeStates)

	if !opts.AutoStartInstances {
		log.Println("Auto-start skipped: controller restarted by daemon")
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
				priorityText = "Empty"
			} else {
				priorityText = strconv.Itoa(*ins.StartPriority)
			}
			log.Printf("  - PRY[%s]: %s", priorityText, ins.Name)
			if err := sp.Start(); err != nil {
				limit := cfg.GetHistoryLimit() * 1024
				sp.Mu.Lock()
				msg := []byte(fmt.Sprintf("\r\n\r\n\x1b[31m\x1b[1m[IpacPanel] %s\x1b[0m\r\n\r\n", err.Error()))
				sp.AppendAndBroadcastLocked(websocket.BinaryMessage, msg, limit)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(ctx)
		cancel()
		serverErr := <-serverErrCh
		api.CleanupUploadTempDir()
		if err := file.CleanupRegisteredAtomicTempDirs(); err != nil {
			return err
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		return serverErr
	case serverErr := <-serverErrCh:
		api.CleanupUploadTempDir()
		if err := file.CleanupRegisteredAtomicTempDirs(); err != nil {
			return err
		}
		return serverErr
	}
}
