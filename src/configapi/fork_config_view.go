package configapi

// Fork addition: what GET /api/config tells an account that may not read the
// configuration.
//
// The route answers every logged-in account, because the home page of a
// plain user needs a few things from it (the routes it may open, the theme).
// Upstream sent such an account nearly everything: the commands of the
// scheduled jobs, the headers routes add for their applications (often a
// key), the API tokens with their hashes, the OpenID clients, the IP
// whitelists, the alerts, the backup repositories, the tunnels. An account
// without the permission to read the configuration now gets that file with
// those parts left out. Accounts that may read the configuration get what
// they got before.

import "github.com/azukaar/cosmos-server/src/utils"

// redactForNonAdmin is the hook of ConfigApiGet, for a caller without
// PERM_CONFIGURATION_READ. The config is a copy read for this answer.
func redactForNonAdmin(config *utils.Config) {
	config.APITokens = nil
	config.CRON = nil
	config.MonitoringAlerts = nil
	config.AdminWhitelistIPs = nil
	config.ShieldWhitelist = nil
	config.HTTPConfig.TrustedProxies = nil
	config.ConstellationConfig.Tunnels = nil
	config.Backup.Backups = nil
	config.BackupOutputDir = ""
	config.IncrBackupOutputDir = ""
	config.Snapshots = utils.SnapshotsConfig{}
	config.Database.PostgresHost = ""
	config.Database.PostgresUsername = ""
	config.Database.PostgresDatabase = ""

	clients := make([]utils.OpenIDClient, len(config.OpenIDClients))
	for i, client := range config.OpenIDClients {
		client.Secret = ""
		clients[i] = client
	}
	config.OpenIDClients = clients

	routes := make([]utils.ProxyRouteConfig, len(config.HTTPConfig.ProxyConfig.Routes))
	for i, route := range config.HTTPConfig.ProxyConfig.Routes {
		route.ExtraHeaders = nil
		route.WhitelistInboundIPs = nil
		routes[i] = route
	}
	config.HTTPConfig.ProxyConfig.Routes = routes
}
