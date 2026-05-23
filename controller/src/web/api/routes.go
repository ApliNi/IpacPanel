package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"
	"errors"
	"net/http"
)

type Route struct {
	Path    string
	Policy  authz.RoutePolicy
	Handler http.HandlerFunc
}

var (
	publicGet  = authz.RoutePolicy{Methods: []string{http.MethodGet}, Auth: authz.AuthModeNone, CSRF: authz.CSRFModeNone, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	publicPost = authz.RoutePolicy{Methods: []string{http.MethodPost}, Auth: authz.AuthModeNone, CSRF: authz.CSRFModeNone, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	userGet    = authz.RoutePolicy{Methods: []string{http.MethodGet}, Auth: authz.AuthModeUser, CSRF: authz.CSRFModeNone, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	userPost   = authz.RoutePolicy{Methods: []string{http.MethodPost}, Auth: authz.AuthModeUser, CSRF: authz.CSRFModeHeader, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	adminGet   = authz.RoutePolicy{Methods: []string{http.MethodGet}, Auth: authz.AuthModeAdmin, CSRF: authz.CSRFModeNone, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	adminPost  = authz.RoutePolicy{Methods: []string{http.MethodPost}, Auth: authz.AuthModeAdmin, CSRF: authz.CSRFModeHeader, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	userSSE    = authz.RoutePolicy{Methods: []string{http.MethodPost}, Auth: authz.AuthModeUser, CSRF: authz.CSRFModeHeader, Origin: authz.OriginModeNone, Kind: authz.RouteKindSSE}
	userWS     = authz.RoutePolicy{Methods: []string{http.MethodGet}, Auth: authz.AuthModeUser, CSRF: authz.CSRFModeWebSocketProtocol, Origin: authz.OriginModeWebSocket, Kind: authz.RouteKindWebSocket}

	// Dashboard endpoints keep public-dashboard fallback in handlers. Snapshot does
	// not require CSRF. Events use optional auth: no auth cookie may fall back to
	// public dashboard, a valid auth cookie must pass header CSRF, and an invalid
	// auth cookie is rejected by Guard before public fallback.
	dashboardSnapshot = authz.RoutePolicy{Methods: []string{http.MethodPost}, Auth: authz.AuthModeOptional, CSRF: authz.CSRFModeNone, Origin: authz.OriginModeNone, Kind: authz.RouteKindAPI}
	dashboardEvents   = authz.RoutePolicy{Methods: []string{http.MethodPost}, Auth: authz.AuthModeOptional, CSRF: authz.CSRFModeHeaderWhenAuthenticated, Origin: authz.OriginModeNone, Kind: authz.RouteKindSSE}
)

var Routes = []Route{
	// Admin
	{Path: "/api/admin/get", Policy: adminGet, Handler: HandleApiAdminGet},
	{Path: "/api/admin/create", Policy: adminPost, Handler: HandleApiAdminCreate},
	{Path: "/api/admin/update", Policy: adminPost, Handler: HandleApiAdminUpdate},
	{Path: "/api/admin/delete", Policy: adminPost, Handler: HandleApiAdminDelete},

	// Group
	{Path: "/api/group/update", Policy: adminPost, Handler: HandleApiGroupUpdate},

	// Settings
	{Path: "/api/settings/public", Policy: publicGet, Handler: HandleApiSettingsPublic},
	{Path: "/api/settings/get", Policy: adminGet, Handler: HandleApiSettingsGet},
	{Path: "/api/settings/update", Policy: adminPost, Handler: HandleApiSettingsUpdate},
	{Path: "/api/settings/restart-controller", Policy: adminPost, Handler: HandleApiSettingsRestartController},

	// Dashboard supports public access when public dashboard is enabled; handlers
	// trust only the Guard-populated request context for authenticated users.
	{Path: "/api/dashboard/events", Policy: dashboardEvents, Handler: HandleApiDashboardEvents},
	{Path: "/api/dashboard/snapshot", Policy: dashboardSnapshot, Handler: HandleApiDashboardSnapshot},

	// Auth
	{Path: "/api/auth/pow", Policy: publicGet, Handler: HandleApiAuthPow},
	{Path: "/api/auth/login", Policy: publicPost, Handler: HandleApiAuthLogin},
	{Path: "/api/auth/logout", Policy: userPost, Handler: HandleApiAuthLogout},
	{Path: "/api/auth/reset", Policy: userPost, Handler: HandleApiAuthReset},

	// File
	{Path: "/api/file/list", Policy: userPost, Handler: HandleApiFileList},
	{Path: "/api/file/read", Policy: userPost, Handler: HandleApiFileRead},
	{Path: "/api/file/raw", Policy: userGet, Handler: HandleApiFileRaw},
	{Path: "/api/file/save", Policy: userPost, Handler: HandleApiFileSave},
	{Path: "/api/file/create/file", Policy: userPost, Handler: HandleApiFileCreateFile},
	{Path: "/api/file/create/dir", Policy: userPost, Handler: HandleApiFileCreateDir},
	{Path: "/api/file/rename", Policy: userPost, Handler: HandleApiFileRename},
	{Path: "/api/file/delete", Policy: userPost, Handler: HandleApiFileDelete},
	{Path: "/api/file/upload/init", Policy: userPost, Handler: HandleApiFileUploadInit},
	{Path: "/api/file/upload/chunk", Policy: userPost, Handler: HandleApiFileUploadChunk},
	{Path: "/api/file/upload/abort", Policy: userPost, Handler: HandleApiFileUploadAbort},
	{Path: "/api/file/upload/complete", Policy: userPost, Handler: HandleApiFileUploadComplete},
	{Path: "/api/file/batch", Policy: userSSE, Handler: HandleApiFileBatch},
	{Path: "/api/file/extract", Policy: userSSE, Handler: HandleApiFileExtract},

	// Controller update
	{Path: "/api/controller/update/status", Policy: adminGet, Handler: HandleApiControllerUpdateStatus},
	{Path: "/api/controller/update/upload/init", Policy: adminPost, Handler: HandleApiControllerUpdateUploadInit},
	{Path: "/api/controller/update/upload/chunk", Policy: adminPost, Handler: HandleApiControllerUpdateUploadChunk},
	{Path: "/api/controller/update/upload/abort", Policy: adminPost, Handler: HandleApiControllerUpdateUploadAbort},
	{Path: "/api/controller/update/upload/complete", Policy: adminPost, Handler: HandleApiControllerUpdateUploadComplete},
	{Path: "/api/controller/update/apply", Policy: adminPost, Handler: HandleApiControllerUpdateApply},

	// Instance
	{Path: "/api/instance/events", Policy: userSSE, Handler: HandleApiInstanceEvents},
	{Path: "/api/instance/get", Policy: userPost, Handler: HandleApiInstanceGet},
	{Path: "/api/instance/create", Policy: adminPost, Handler: HandleApiInstanceCreate},
	{Path: "/api/instance/update", Policy: adminPost, Handler: HandleApiInstanceUpdate},
	{Path: "/api/instance/delete", Policy: adminPost, Handler: HandleApiInstanceDelete},
	{Path: "/api/instance/control", Policy: userPost, Handler: HandleApiInstanceControl},
	{Path: "/api/instance/ws", Policy: userWS, Handler: HandleApiInstanceWs},

	// User
	{Path: "/api/user/get", Policy: userGet, Handler: HandleApiUserGet},
	{Path: "/api/user/list", Policy: adminGet, Handler: HandleApiUserList},
	{Path: "/api/user/update", Policy: userPost, Handler: HandleApiUserUpdate},
}

func RegisterRoutes(mux *http.ServeMux) {
	if mux == nil {
		panic(errors.New(msg.RegisterRoutesNilMux))
	}
	for _, route := range Routes {
		if err := route.Policy.Validate(); err != nil {
			panic(errors.New(route.Path + ": " + err.Error()))
		}
		mux.HandleFunc(route.Path, routeHandler(route))
	}
}

func routeHandler(route Route) http.HandlerFunc {
	guardedHandler := authz.Wrap(route.Handler, authz.DefaultRuntime.GuardConfig(route.Policy, web.WriteAPIError, web.MarkRequestUser))
	return func(w http.ResponseWriter, r *http.Request) {
		web.MarkRequestRouteKind(w, string(route.Policy.Kind))
		guardedHandler(w, r)
	}
}
