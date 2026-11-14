package storage

// Fork addition: listing the host's folders needs the permission of the forms
// that use the file picker, not only the read permission.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestListDirectoryNeedsResources(t *testing.T) {
	cases := []struct {
		name  string
		auth  *utils.AuthContext
		allow bool
	}{
		{"read only", &utils.AuthContext{Nickname: "viewer", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ}}, false},
		{"read-only token", &utils.AuthContext{Nickname: "admin", APIToken: &utils.APITokenContext{Name: "monitor", Permissions: []utils.Permission{utils.PERM_RESOURCES_READ}}}, false},
		{"resources", &utils.AuthContext{Nickname: "admin", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES}, IsSudoed: true}, true},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/cosmos/api/list-dir?storage=local&path="+t.TempDir(), nil)
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, c.auth))
		rec := httptest.NewRecorder()
		ListDirectoryRoute(rec, req)
		refused := rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden
		if refused == c.allow {
			t.Errorf("%s: status %d", c.name, rec.Code)
		}
	}
}
