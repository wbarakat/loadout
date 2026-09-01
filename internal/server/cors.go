package server

import "net/http"

// allowedCORSMethods and allowedCORSHeaders list exactly what a
// browser preflight is told it may use against loadoutd: the
// methods and headers its routes actually need. No route needs any
// other method or header, so this list stays fixed.
const (
	allowedCORSMethods   = "GET, POST, OPTIONS"
	allowedCORSHeaders   = "Authorization, Content-Type, X-Loadout-Parent"
	corsPreflightMaxAge  = "600"
	corsHeaderOrigin     = "Access-Control-Allow-Origin"
	corsHeaderVary       = "Vary"
	corsHeaderMethods    = "Access-Control-Allow-Methods"
	corsHeaderReqHeaders = "Access-Control-Allow-Headers"
	corsHeaderMaxAge     = "Access-Control-Max-Age"
)

// corsMiddleware adds opt-in CORS support so a browser dashboard,
// served from a different origin than loadoutd, can call this
// server's API.
//
// When allowedOrigin is empty, CORS stays off: corsMiddleware is a
// pure pass-through to next. This is the default, so a self-hosted
// server never answers cross-origin browser requests unless its
// operator opts in.
//
// When allowedOrigin is set, corsMiddleware wraps the whole mux, so
// it runs before auth:
//   - Every OPTIONS request is answered here, directly, with 204
//     and the CORS headers, and never reaches auth or a route
//     handler. A real browser preflight carries no Authorization
//     header, so it must never be asked for one; and its response
//     carries only headers, never a body, so a preflight can never
//     leak data.
//   - Every other request gets Access-Control-Allow-Origin set to
//     the exact configured origin (never "*") plus Vary: Origin,
//     then proceeds to auth and the route as normal. Enabling CORS
//     never weakens auth: a real request to /v1/* still needs its
//     bearer token.
func corsMiddleware(next http.Handler, allowedOrigin string) http.Handler {
	if allowedOrigin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(corsHeaderOrigin, allowedOrigin)
		w.Header().Set(corsHeaderVary, "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set(corsHeaderMethods, allowedCORSMethods)
			w.Header().Set(corsHeaderReqHeaders, allowedCORSHeaders)
			w.Header().Set(corsHeaderMaxAge, corsPreflightMaxAge)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
