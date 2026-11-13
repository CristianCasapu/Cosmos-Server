package utils

// Fork addition: what the API answers on a hostname that is not the server's
// own.
//
// Every hostname of a route reaches this server, and the pages of an
// application are at home on that hostname. The API belongs to the server's
// own hostname (EnsureHostnameCosmosAPI); elsewhere only what a sign-in
// needs is answered. Upstream decided that by prefix, which also let through
// every path that merely starts like one of them, and everything under
// /cosmos that is not under /cosmos/api. This is the exact list:
//
//   - signing in and out, and what the sign-in pages ask: login, logout,
//     status, password-reset, can-send-email, me;
//   - giving the code of the second factor (POST /cosmos/api/mfa). Setting a
//     second factor up, or removing one, is done on the server's own hostname;
//   - /cosmos/oauth2/..., the last step of the sign-in of an application.

import "strings"

// cosmosPathOnAnyHostname is the hook of EnsureHostnameCosmosAPI.
func cosmosPathOnAnyHostname(path string, method string) bool {
	switch path {
	case "/cosmos/api/login",
		"/cosmos/api/logout",
		"/cosmos/api/status",
		"/cosmos/api/password-reset",
		"/cosmos/api/can-send-email",
		"/cosmos/api/me":
		return true
	case "/cosmos/api/mfa":
		return method == "POST"
	}
	return strings.HasPrefix(path, "/cosmos/oauth2/")
}
