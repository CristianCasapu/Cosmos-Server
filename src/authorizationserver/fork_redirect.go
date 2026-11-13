package authorizationserver

// Fork addition: where the sign-in of an application ends.
//
// After the sign-in the browser is sent back to the path it first asked for
// on the application's own address. That path is only ever a path: a value
// that a browser would read as another site ("//host", "/\host", anything
// with a backslash or a control character) is replaced by "/".

import "strings"

// localPath is the hook of detectCallbackEndpoint.
func localPath(path string) string {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/"
	}
	for _, c := range path {
		if c == '\\' || c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	return path
}
