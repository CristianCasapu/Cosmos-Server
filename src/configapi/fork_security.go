package configapi

// Fork addition: what the settings code reports to the security record
// (utils/fork_security.go, package security).

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// maxChangedSections bounds the list of sections a settings entry names.
const maxChangedSections = 12

// auditSettings records that the settings were saved, and which sections
// differ from what was stored. Only the names of the sections are kept,
// never a value.
func auditSettings(req *http.Request, before utils.Config, after utils.Config) {
	sections := changedSections(before, after)
	if len(sections) > maxChangedSections {
		sections = append(sections[:maxChangedSections], "…")
	}
	changed := strings.Join(sections, ", ")
	if changed == "" {
		changed = "(none)"
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", changed)
}

// changedSections names the top-level settings that differ between two
// configurations. The HTTP settings, which hold most of what there is, are
// named one level down ("HTTPConfig.Hostname").
func changedSections(before utils.Config, after utils.Config) []string {
	changed := changedFields("", reflect.ValueOf(before), reflect.ValueOf(after))
	sort.Strings(changed)
	return changed
}

func changedFields(prefix string, before reflect.Value, after reflect.Value) []string {
	changed := []string{}
	for i := 0; i < before.NumField(); i++ {
		field := before.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		a, b := before.Field(i), after.Field(i)
		if prefix == "" && field.Name == "HTTPConfig" && a.Kind() == reflect.Struct {
			changed = append(changed, changedFields("HTTPConfig.", a, b)...)
			continue
		}
		if !sameSetting(a.Interface(), b.Interface()) {
			changed = append(changed, prefix+field.Name)
		}
	}
	return changed
}

// sameSetting compares two values the way a save sees them: through their
// JSON form, where "nothing" (null), an empty list and an empty object are
// the same thing. The form saves the whole configuration back, and what was
// null in the file comes back as [] or {}.
func sameSetting(a interface{}, b interface{}) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(a, b)
	}
	if string(encodedA) == string(encodedB) {
		return true
	}
	var plainA, plainB interface{}
	if json.Unmarshal(encodedA, &plainA) != nil || json.Unmarshal(encodedB, &plainB) != nil {
		return false
	}
	return reflect.DeepEqual(withoutEmpty(plainA), withoutEmpty(plainB))
}

// withoutEmpty replaces empty lists and objects by nil, all the way down,
// and drops the keys of an object that hold nothing.
func withoutEmpty(value interface{}) interface{} {
	switch typed := value.(type) {
	case []interface{}:
		if len(typed) == 0 {
			return nil
		}
		for i := range typed {
			typed[i] = withoutEmpty(typed[i])
		}
		return typed
	case map[string]interface{}:
		for key, inner := range typed {
			if cleaned := withoutEmpty(inner); cleaned == nil {
				delete(typed, key)
			} else {
				typed[key] = cleaned
			}
		}
		if len(typed) == 0 {
			return nil
		}
		return typed
	}
	return value
}

// auditRoutePatch records a change to the URLs made through the settings
// patch: one added, deleted, replaced or moved.
func auditRoutePatch(req *http.Request, update UpdateRouteRequest) {
	name := update.RouteName
	if name == "" && update.NewRoute != nil {
		name = update.NewRoute.Name
	}
	switch update.Operation {
	case "add":
		utils.AuditSecurity(req, utils.SecRouteCreated, name)
	case "delete":
		utils.AuditSecurity(req, utils.SecRouteDeleted, name)
	case "replace", "move_up", "move_down":
		utils.AuditSecurity(req, utils.SecRouteUpdated, name, "operation", update.Operation)
	}
}
