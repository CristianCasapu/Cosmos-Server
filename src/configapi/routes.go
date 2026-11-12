package configapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/constellation"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// managedRouteError rejects edits to owner-managed routes.
func managedRouteError(w http.ResponseWriter, route utils.ProxyRouteConfig, code string) {
	msg := "Route \"" + route.Name + "\" is managed by " + route.ManagedBy() + " and can only be edited from there"
	utils.Error("Routes: "+msg, nil)
	utils.HTTPError(w, msg, http.StatusConflict, code)
}

// stripOwner clears the owner pair from a user-supplied route.
func stripOwner(route utils.ProxyRouteConfig) utils.ProxyRouteConfig {
	route.ManagedByKind, route.ManagedByName, route.ManagedByVersion = "", "", 0
	return route
}

// validateRoute returns an error message when the route is invalid, "" otherwise.
func validateRoute(route utils.ProxyRouteConfig) string {
	if !utils.IsValidLBMode(route.LBMode) {
		return "Unsupported load balancing mode \"" + route.LBMode + "\" on route \"" + route.Name +
			"\". Supported modes are \"round_robin\", \"load_based\" and \"first\" (an empty value means \"first\": always send traffic to the first/local target)"
	}
	return ""
}

func RoutesRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		listRoutes(w, req)
	} else if req.Method == "POST" {
		createRoute(w, req)
	} else {
		utils.Error("RoutesRoute: Method not allowed "+req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
	}
}

func RoutesIdRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		getRoute(w, req)
	} else if req.Method == "PUT" {
		updateRoute(w, req)
	} else if req.Method == "DELETE" {
		deleteRoute(w, req)
	} else {
		utils.Error("RoutesIdRoute: Method not allowed "+req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
	}
}

// listRoutes godoc
// @Summary List all proxy routes
// @Description Returns all configured proxy routes
// @Tags routes
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse{data=[]utils.ProxyRouteConfig}
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Router /api/routes [get]
func listRoutes(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}

	config := utils.ReadConfigFromFile()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   config.HTTPConfig.ProxyConfig.Routes,
	})
}

// getRoute godoc
// @Summary Get a proxy route by name
// @Description Returns a single proxy route configuration by its name
// @Tags routes
// @Produce json
// @Security BearerAuth
// @Param name path string true "Route name"
// @Success 200 {object} utils.APIResponse{data=utils.ProxyRouteConfig}
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 404 {object} utils.HTTPErrorResult
// @Router /api/routes/{name} [get]
func getRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}

	name := mux.Vars(req)["name"]
	config := utils.ReadConfigFromFile()

	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if route.Name == name {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "OK",
				"data":   route,
			})
			return
		}
	}

	utils.HTTPError(w, "Route not found", http.StatusNotFound, "RT001")
}

// createRoute godoc
// @Summary Create a new proxy route
// @Description Creates a new proxy route configuration
// @Tags routes
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body utils.ProxyRouteConfig true "Route configuration"
// @Success 200 {object} utils.APIResponse{data=utils.ProxyRouteConfig}
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/routes [post]
func createRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}

	var newRoute utils.ProxyRouteConfig
	err := json.NewDecoder(req.Body).Decode(&newRoute)
	if err != nil {
		utils.Error("CreateRoute: Invalid request", err)
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "RT002")
		return
	}

	if newRoute.Name == "" {
		utils.HTTPError(w, "Route name is required", http.StatusBadRequest, "RT003")
		return
	}

	if !newRoute.UseHost && !newRoute.UsePathPrefix {
		utils.HTTPError(w, "Route must have at least one of UseHost or UsePathPrefix enabled, otherwise it will catch all requests", http.StatusBadRequest, "RT008")
		return
	}

	if msg := validateRoute(newRoute); msg != "" {
		utils.Error("CreateRoute: "+msg, nil)
		utils.HTTPError(w, msg, http.StatusBadRequest, "RT009")
		return
	}
	newRoute = stripOwner(newRoute)

	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()

	config := utils.ReadConfigFromFile()
	routes := config.HTTPConfig.ProxyConfig.Routes

	for _, route := range routes {
		if route.Name == newRoute.Name {
			utils.HTTPError(w, "Route with this name already exists", http.StatusConflict, "RT004")
			return
		}
	}

	config.HTTPConfig.ProxyConfig.Routes = append([]utils.ProxyRouteConfig{newRoute}, routes...)
	utils.SetBaseMainConfig(config)

	utils.Log("Route created: " + newRoute.Name)

	utils.TriggerEvent(
		"cosmos.routes",
		"Route created: "+newRoute.Name,
		"success",
		"",
		map[string]interface{}{})

	go func() {
		utils.RestartHTTPServer()
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   newRoute,
	})
}

// updateRoute godoc
// @Summary Update a proxy route
// @Description Replaces an existing proxy route configuration by name
// @Tags routes
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Route name"
// @Param request body utils.ProxyRouteConfig true "Updated route configuration"
// @Success 200 {object} utils.APIResponse{data=utils.ProxyRouteConfig}
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 404 {object} utils.HTTPErrorResult
// @Router /api/routes/{name} [put]
func updateRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}

	name := mux.Vars(req)["name"]

	var updatedRoute utils.ProxyRouteConfig
	err := json.NewDecoder(req.Body).Decode(&updatedRoute)
	if err != nil {
		utils.Error("UpdateRoute: Invalid request", err)
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "RT005")
		return
	}

	if !updatedRoute.UseHost && !updatedRoute.UsePathPrefix {
		utils.HTTPError(w, "Route must have at least one of UseHost or UsePathPrefix enabled, otherwise it will catch all requests", http.StatusBadRequest, "RT008")
		return
	}

	if msg := validateRoute(updatedRoute); msg != "" {
		utils.Error("UpdateRoute: "+msg, nil)
		utils.HTTPError(w, msg, http.StatusBadRequest, "RT009")
		return
	}
	updatedRoute = stripOwner(updatedRoute)

	utils.ConfigLock.Lock()

	config := utils.ReadConfigFromFile()
	routes := config.HTTPConfig.ProxyConfig.Routes
	routeIndex := -1

	for i, route := range routes {
		if route.Name == name {
			routeIndex = i
			break
		}
	}

	if routeIndex == -1 {
		// Not in local config: dispatch the edit to the tunnel's origin nodes (they take their own lock).
		utils.ConfigLock.Unlock()
		dispatchTunnelRouteOp(w, constellation.RouteOpRequest{Op: constellation.RouteOpUpdate, Name: name, Route: &updatedRoute}, "RT006")
		return
	}

	if routes[routeIndex].IsManaged() {
		utils.ConfigLock.Unlock()
		managedRouteError(w, routes[routeIndex], "RT010")
		return
	}

	routes[routeIndex] = updatedRoute
	config.HTTPConfig.ProxyConfig.Routes = routes
	utils.SetBaseMainConfig(config)
	utils.ConfigLock.Unlock()

	utils.Log("Route updated: " + name)

	utils.TriggerEvent(
		"cosmos.routes",
		"Route updated: "+name,
		"success",
		"",
		map[string]interface{}{})

	go func() {
		utils.RestartHTTPServer()
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   updatedRoute,
	})
}

// dispatchTunnelRouteOp forwards an edit of a tunnel-advertised route to its advertisers; notFoundCode preserves the caller's 404 code.
func dispatchTunnelRouteOp(w http.ResponseWriter, op constellation.RouteOpRequest, notFoundCode string) {
	tunnel, ok := constellation.FindLocalTunnel(op.Name)
	if !ok {
		utils.HTTPError(w, "Route not found", http.StatusNotFound, notFoundCode)
		return
	}
	if tunnel.Route.IsManaged() {
		managedRouteError(w, tunnel.Route, "RT010")
		return
	}

	acked, err := constellation.DispatchTunnelRouteOp(op)
	if err != nil {
		msg := "Route \"" + op.Name + "\" is tunneled from other nodes and the edit could not be applied everywhere: " + err.Error()
		if len(acked) > 0 {
			msg += " (applied on " + strings.Join(acked, ", ") + ")"
		}
		utils.Error("Routes: "+msg, nil)
		utils.HTTPError(w, msg, http.StatusBadGateway, "RT012")
		return
	}

	utils.Log("Tunneled route " + op.Op + " dispatched for " + op.Name + " to " + strings.Join(acked, ", "))

	utils.TriggerEvent(
		"cosmos.routes",
		"Tunneled route "+op.Op+"d on "+strings.Join(acked, ", ")+": "+op.Name,
		"success",
		"",
		map[string]interface{}{"nodes": acked})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   op.Route,
		"nodes":  acked,
	})
}

// deleteRoute godoc
// @Summary Delete a proxy route
// @Description Removes a proxy route configuration by name
// @Tags routes
// @Produce json
// @Security BearerAuth
// @Param name path string true "Route name"
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 404 {object} utils.HTTPErrorResult
// @Router /api/routes/{name} [delete]
func deleteRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}

	name := mux.Vars(req)["name"]

	utils.ConfigLock.Lock()

	config := utils.ReadConfigFromFile()
	routes := config.HTTPConfig.ProxyConfig.Routes
	routeIndex := -1

	for i, route := range routes {
		if route.Name == name {
			routeIndex = i
			break
		}
	}

	if routeIndex == -1 {
		utils.ConfigLock.Unlock()
		dispatchTunnelRouteOp(w, constellation.RouteOpRequest{Op: constellation.RouteOpDelete, Name: name}, "RT007")
		return
	}

	if routes[routeIndex].IsManaged() {
		utils.ConfigLock.Unlock()
		managedRouteError(w, routes[routeIndex], "RT010")
		return
	}

	routes = append(routes[:routeIndex], routes[routeIndex+1:]...)
	config.HTTPConfig.ProxyConfig.Routes = routes
	utils.SetBaseMainConfig(config)
	utils.ConfigLock.Unlock()

	utils.Log("Route deleted: " + name)

	utils.TriggerEvent(
		"cosmos.routes",
		"Route deleted: "+name,
		"success",
		"",
		map[string]interface{}{})

	go func() {
		utils.RestartHTTPServer()
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
	})
}
