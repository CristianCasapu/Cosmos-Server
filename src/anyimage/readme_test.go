package anyimage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The real descriptions in testdata/readmes were saved once from Docker
// Hub's API (full_description only, 2026-11-15). The expected variables were
// written by reading each text: the settings the text documents for this
// image (not those of another service in its compose example, not *_FILE /
// FILE__ variants, which give the same setting from a file), and which ones
// the text itself calls required without a condition.
type fixtureWant struct {
	repo     string
	env      []string
	required []string
	volumes  []string // a few that must be found
	ports    []string
}

var bitnamiPostgres = strings.Fields(`POSTGRESQL_VOLUME_DIR POSTGRESQL_DATA_DIR POSTGRESQL_EXTRA_FLAGS POSTGRESQL_INIT_MAX_TIMEOUT
	POSTGRESQL_PGCTLTIMEOUT POSTGRESQL_SHUTDOWN_MODE POSTGRESQL_CLUSTER_APP_NAME POSTGRESQL_DATABASE POSTGRESQL_INITDB_ARGS
	ALLOW_EMPTY_PASSWORD POSTGRESQL_INITDB_WAL_DIR POSTGRESQL_MASTER_HOST POSTGRESQL_MASTER_PORT_NUMBER
	POSTGRESQL_NUM_SYNCHRONOUS_REPLICAS POSTGRESQL_SYNCHRONOUS_REPLICAS_MODE POSTGRESQL_PORT_NUMBER
	POSTGRESQL_ALLOW_REMOTE_CONNECTIONS POSTGRESQL_REPLICATION_MODE POSTGRESQL_REPLICATION_USER
	POSTGRESQL_REPLICATION_USE_PASSFILE POSTGRESQL_REPLICATION_PASSFILE_PATH POSTGRESQL_SR_CHECK POSTGRESQL_SR_CHECK_USERNAME
	POSTGRESQL_SR_CHECK_DATABASE POSTGRESQL_SYNCHRONOUS_COMMIT_MODE POSTGRESQL_FSYNC POSTGRESQL_USERNAME POSTGRESQL_ENABLE_LDAP
	POSTGRESQL_LDAP_URL POSTGRESQL_LDAP_PREFIX POSTGRESQL_LDAP_SUFFIX POSTGRESQL_LDAP_SERVER POSTGRESQL_LDAP_PORT
	POSTGRESQL_LDAP_SCHEME POSTGRESQL_LDAP_TLS POSTGRESQL_LDAP_BASE_DN POSTGRESQL_LDAP_BIND_DN POSTGRESQL_LDAP_BIND_PASSWORD
	POSTGRESQL_LDAP_BIND_USE_PASSFILE POSTGRESQL_LDAP_BIND_PASSFILE_PATH POSTGRESQL_LDAP_SEARCH_ATTR
	POSTGRESQL_LDAP_SEARCH_FILTER POSTGRESQL_INITSCRIPTS_USERNAME POSTGRESQL_PASSWORD POSTGRESQL_POSTGRES_PASSWORD
	POSTGRESQL_REPLICATION_PASSWORD POSTGRESQL_SR_CHECK_PASSWORD POSTGRESQL_INITSCRIPTS_PASSWORD POSTGRESQL_ENABLE_TLS
	POSTGRESQL_TLS_CERT_FILE POSTGRESQL_TLS_KEY_FILE POSTGRESQL_TLS_CA_FILE POSTGRESQL_TLS_CRL_FILE
	POSTGRESQL_TLS_PREFER_SERVER_CIPHERS POSTGRESQL_SHARED_PRELOAD_LIBRARIES POSTGRESQL_PGAUDIT_LOG
	POSTGRESQL_PGAUDIT_LOG_CATALOG POSTGRESQL_PGAUDIT_LOG_PARAMETER POSTGRESQL_LOG_CONNECTIONS POSTGRESQL_LOG_DISCONNECTIONS
	POSTGRESQL_LOG_HOSTNAME POSTGRESQL_CLIENT_MIN_MESSAGES POSTGRESQL_LOG_LINE_PREFIX POSTGRESQL_LOG_TIMEZONE
	POSTGRESQL_TIMEZONE POSTGRESQL_MAX_CONNECTIONS POSTGRESQL_TCP_KEEPALIVES_IDLE POSTGRESQL_TCP_KEEPALIVES_INTERVAL
	POSTGRESQL_TCP_KEEPALIVES_COUNT POSTGRESQL_STATEMENT_TIMEOUT POSTGRESQL_PGHBA_AUTH_METHOD POSTGRESQL_PGHBA_REMOVE_FILTERS
	POSTGRESQL_USERNAME_CONNECTION_LIMIT POSTGRESQL_POSTGRES_CONNECTION_LIMIT POSTGRESQL_WAL_LEVEL
	POSTGRESQL_DEFAULT_TOAST_COMPRESSION POSTGRESQL_PASSWORD_ENCRYPTION POSTGRESQL_DEFAULT_TRANSACTION_ISOLATION
	POSTGRESQL_REPLICATION_NODES POSTGRESQL_PERFORM_RESTORE`)

var fixtures = map[string]fixtureWant{
	"library_postgres.md": {repo: "library/postgres",
		env:      strings.Fields("POSTGRES_PASSWORD POSTGRES_USER POSTGRES_DB POSTGRES_INITDB_ARGS POSTGRES_INITDB_WALDIR POSTGRES_HOST_AUTH_METHOD PGDATA LANG"),
		required: []string{"POSTGRES_PASSWORD"}},
	"library_mysql.md": {repo: "library/mysql",
		env:      strings.Fields("MYSQL_ROOT_PASSWORD MYSQL_DATABASE MYSQL_USER MYSQL_PASSWORD MYSQL_ALLOW_EMPTY_PASSWORD MYSQL_RANDOM_ROOT_PASSWORD MYSQL_ONETIME_PASSWORD MYSQL_INITDB_SKIP_TZINFO MYSQL_ROOT_HOST"),
		required: []string{"MYSQL_ROOT_PASSWORD"}, volumes: []string{"/var/lib/mysql", "/etc/mysql/conf.d"}},
	// "One of … is required": none alone
	"library_mariadb.md": {repo: "library/mariadb",
		env:     strings.Fields("MARIADB_ROOT_PASSWORD MARIADB_ALLOW_EMPTY_ROOT_PASSWORD MARIADB_RANDOM_ROOT_PASSWORD MARIADB_USER MARIADB_PASSWORD MARIADB_DATABASE MARIADB_AUTO_UPGRADE MARIADB_ROOT_PASSWORD_HASH MARIADB_DISABLE_UPGRADE_BACKUP"),
		volumes: []string{"/var/lib/mysql"}},
	// mongo-express's ME_CONFIG_* belong to the other service of the compose example
	"library_mongo.md": {repo: "library/mongo",
		env: strings.Fields("MONGO_INITDB_ROOT_USERNAME MONGO_INITDB_ROOT_PASSWORD MONGO_INITDB_DATABASE")},
	// SKIP_FIX_PERMS was missed in the first reading and found by the parser; the text has it
	"library_redis.md": {repo: "library/redis", env: []string{"SKIP_DROP_PRIVS", "SKIP_FIX_PERMS"}},
	// the compose example's db service (mariadb) is not nextcloud's
	"library_nextcloud.md": {repo: "library/nextcloud",
		env: strings.Fields(`SQLITE_DATABASE MYSQL_DATABASE MYSQL_USER MYSQL_PASSWORD MYSQL_HOST POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD
			POSTGRES_HOST NEXTCLOUD_ADMIN_USER NEXTCLOUD_ADMIN_PASSWORD NEXTCLOUD_DATA_DIR NEXTCLOUD_TRUSTED_DOMAINS NEXTCLOUD_UPDATE
			NEXTCLOUD_INIT_HTACCESS REDIS_HOST REDIS_HOST_PORT REDIS_HOST_PASSWORD SMTP_HOST SMTP_SECURE SMTP_PORT SMTP_AUTHTYPE
			SMTP_NAME SMTP_PASSWORD MAIL_FROM_ADDRESS MAIL_DOMAIN OBJECTSTORE_S3_BUCKET OBJECTSTORE_S3_REGION OBJECTSTORE_S3_HOST
			OBJECTSTORE_S3_PORT OBJECTSTORE_S3_KEY OBJECTSTORE_S3_SECRET OBJECTSTORE_S3_STORAGE_CLASS OBJECTSTORE_S3_SSL
			OBJECTSTORE_S3_USEPATH_STYLE OBJECTSTORE_S3_LEGACYAUTH OBJECTSTORE_S3_OBJECT_PREFIX OBJECTSTORE_S3_AUTOCREATE
			OBJECTSTORE_S3_SSE_C_KEY OBJECTSTORE_SWIFT_URL OBJECTSTORE_SWIFT_AUTOCREATE OBJECTSTORE_SWIFT_USER_NAME
			OBJECTSTORE_SWIFT_USER_PASSWORD OBJECTSTORE_SWIFT_USER_DOMAIN OBJECTSTORE_SWIFT_PROJECT_NAME
			OBJECTSTORE_SWIFT_PROJECT_DOMAIN OBJECTSTORE_SWIFT_SERVICE_NAME OBJECTSTORE_SWIFT_REGION
			OBJECTSTORE_SWIFT_CONTAINER_NAME PHP_MEMORY_LIMIT PHP_UPLOAD_LIMIT APACHE_BODY_LIMIT APACHE_DISABLE_REWRITE_IP
			TRUSTED_PROXIES OVERWRITEHOST OVERWRITEPROTOCOL OVERWRITECLIURL OVERWRITEWEBROOT OVERWRITECONDADDR`),
		volumes: []string{"/var/www/html"}, ports: []string{"80/tcp"}},
	"bitnami_postgresql.md": {repo: "bitnami/postgresql", env: bitnamiPostgres},
	"bitnami_mariadb.md": {repo: "bitnami/mariadb",
		env: strings.Fields(`ALLOW_EMPTY_PASSWORD MARIADB_AUTHENTICATION_PLUGIN MARIADB_ROOT_USER MARIADB_ROOT_PASSWORD MARIADB_USER
			MARIADB_PASSWORD MARIADB_DATABASE MARIADB_MASTER_HOST MARIADB_MASTER_PORT_NUMBER MARIADB_MASTER_ROOT_USER
			MARIADB_MASTER_ROOT_PASSWORD MARIADB_MASTER_DELAY MARIADB_REPLICATION_USER MARIADB_REPLICATION_PASSWORD
			MARIADB_PORT_NUMBER MARIADB_REPLICATION_MODE MARIADB_REPLICATION_SLAVE_DUMP MARIADB_EXTRA_FLAGS
			MARIADB_INIT_SLEEP_TIME MARIADB_CHARACTER_SET MARIADB_COLLATE MARIADB_BIND_ADDRESS MARIADB_SQL_MODE MARIADB_UPGRADE
			MARIADB_SKIP_TEST_DB MARIADB_CLIENT_ENABLE_SSL MARIADB_REPLICATION_USE_SSL MARIADB_CLIENT_SSL_CA_FILE
			MARIADB_CLIENT_SSL_CERT_FILE MARIADB_CLIENT_SSL_KEY_FILE MARIADB_CLIENT_EXTRA_FLAGS MARIADB_STARTUP_WAIT_RETRIES
			MARIADB_STARTUP_WAIT_SLEEP_TIME MARIADB_ENABLE_SLOW_QUERY MARIADB_LONG_QUERY_TIME`)},
	"bitnami_redis.md": {repo: "bitnami/redis",
		env: strings.Fields(`REDIS_DATA_DIR REDIS_OVERRIDES_FILE REDIS_DISABLE_COMMANDS REDIS_DATABASE REDIS_AOF_ENABLED REDIS_RDB_POLICY
			REDIS_RDB_POLICY_DISABLED REDIS_MASTER_HOST REDIS_MASTER_PORT_NUMBER REDIS_PORT_NUMBER REDIS_ALLOW_REMOTE_CONNECTIONS
			REDIS_REPLICATION_MODE REDIS_REPLICA_IP REDIS_REPLICA_PORT REDIS_EXTRA_FLAGS ALLOW_EMPTY_PASSWORD REDIS_PASSWORD
			REDIS_MASTER_PASSWORD REDIS_ACLFILE REDIS_IO_THREADS_DO_READS REDIS_IO_THREADS REDIS_TLS_ENABLED REDIS_TLS_PORT_NUMBER
			REDIS_TLS_CERT_FILE REDIS_TLS_CA_DIR REDIS_TLS_KEY_FILE REDIS_TLS_KEY_FILE_PASS REDIS_TLS_CA_FILE
			REDIS_TLS_DH_PARAMS_FILE REDIS_TLS_AUTH_CLIENTS REDIS_SENTINEL_MASTER_NAME REDIS_SENTINEL_HOST
			REDIS_SENTINEL_PORT_NUMBER REDIS_SENTINEL_PASSWORD OPENSSL_FIPS`)},
	"bitnami_mongodb.md": {repo: "bitnami/mongodb",
		env: strings.Fields(`MONGODB_MOUNTED_CONF_DIR MONGODB_INIT_RETRY_ATTEMPTS MONGODB_INIT_RETRY_DELAY MONGODB_PORT_NUMBER
			MONGODB_EXTRA_FLAGS MONGODB_ENABLE_NUMACTL MONGODB_SHELL_EXTRA_FLAGS MONGODB_ADVERTISED_HOSTNAME MONGODB_ADVERTISE_IP
			MONGODB_ADVERTISED_PORT_NUMBER MONGODB_DISABLE_JAVASCRIPT MONGODB_ENABLE_JOURNAL MONGODB_DISABLE_SYSTEM_LOG
			MONGODB_ENABLE_DIRECTORY_PER_DB MONGODB_ENABLE_IPV6 MONGODB_SYSTEM_LOG_VERBOSITY MONGODB_ROOT_USER
			MONGODB_ROOT_PASSWORD MONGODB_USERNAME MONGODB_PASSWORD MONGODB_DATABASE MONGODB_METRICS_USERNAME
			MONGODB_METRICS_PASSWORD MONGODB_EXTRA_USERNAMES MONGODB_EXTRA_PASSWORDS MONGODB_EXTRA_DATABASES
			ALLOW_EMPTY_PASSWORD MONGODB_REPLICA_SET_MODE MONGODB_REPLICA_SET_NAME MONGODB_REPLICA_SET_KEY
			MONGODB_INITIAL_PRIMARY_HOST MONGODB_INITIAL_PRIMARY_PORT_NUMBER MONGODB_INITIAL_PRIMARY_ROOT_PASSWORD
			MONGODB_INITIAL_PRIMARY_ROOT_USER MONGODB_SET_SECONDARY_OK MONGODB_DISABLE_ENFORCE_AUTH MONGODB_CLIENT_EXTRA_FLAGS`)},
	// linuxserver's "unless flagged optional it is mandatory" is boilerplate (PUID, PGID and TZ
	// have defaults): not read as required
	"linuxserver_mariadb.md": {repo: "linuxserver/mariadb",
		env:     strings.Fields("PUID PGID TZ MYSQL_ROOT_PASSWORD MYSQL_DATABASE MYSQL_USER MYSQL_PASSWORD REMOTE_SQL CLI_OPTS UMASK"),
		volumes: []string{"/config"}, ports: []string{"3306/tcp"}},
	"linuxserver_sonarr.md": {repo: "linuxserver/sonarr", env: strings.Fields("PUID PGID TZ UMASK"),
		volumes: []string{"/config", "/tv", "/downloads"}, ports: []string{"8989/tcp"}},
	"linuxserver_nextcloud.md": {repo: "linuxserver/nextcloud", env: strings.Fields("PUID PGID TZ UMASK"),
		volumes: []string{"/config", "/data"}, ports: []string{"443/tcp"}},
	"linuxserver_qbittorrent.md": {repo: "linuxserver/qbittorrent", env: strings.Fields("PUID PGID TZ UMASK WEBUI_PORT TORRENTING_PORT"),
		volumes: []string{"/config", "/downloads"}, ports: []string{"8080/tcp", "6881/tcp", "6881/udp"}},
	// hotio publishes no description on Docker Hub
	"hotio_sonarr.md":       {repo: "hotio/sonarr"},
	"hotio_qbittorrent.md":  {repo: "hotio/qbittorrent"},
	"vaultwarden_server.md": {repo: "vaultwarden/server", volumes: []string{"/data"}, ports: []string{"80/tcp"}},
	"gitea_gitea.md":        {repo: "gitea/gitea"},
	"n8nio_n8n.md": {repo: "n8nio/n8n",
		env:     strings.Fields("DB_TYPE DB_POSTGRESDB_DATABASE DB_POSTGRESDB_HOST DB_POSTGRESDB_PORT DB_POSTGRESDB_USER DB_POSTGRESDB_SCHEMA DB_POSTGRESDB_PASSWORD GENERIC_TIMEZONE TZ"),
		volumes: []string{"/home/node/.n8n"}, ports: []string{"5678/tcp"}},
	"louislam_uptime-kuma.md":   {repo: "louislam/uptime-kuma"},
	"portainer_portainer-ce.md": {repo: "portainer/portainer-ce"},
	// Pi-hole v6's FTLCONF_* names are mixed case: outside the grammar, missed on purpose
	"pihole_pihole.md": {repo: "pihole/pihole", env: strings.Fields("TZ FTLCONF_webserver_api_password FTLCONF_dns_listeningMode"),
		volumes: []string{"/etc/pihole"}, ports: []string{"53/tcp", "53/udp", "80/tcp", "443/tcp"}},
	"grafana_grafana.md":     {repo: "grafana/grafana", ports: []string{"3000/tcp"}},
	"jellyfin_jellyfin.md":   {repo: "jellyfin/jellyfin"},
	"adguard_adguardhome.md": {repo: "adguard/adguardhome", volumes: []string{"/opt/adguardhome/work", "/opt/adguardhome/conf"}, ports: []string{"53/udp", "3000/tcp", "853/tcp"}},
	"syncthing_syncthing.md": {repo: "syncthing/syncthing"},
}

func setOf(list []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range list {
		out[s] = true
	}
	return out
}

func TestDescriptionFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/readmes/*.md")
	if len(files) < 15 {
		t.Fatalf("%d fixtures", len(files))
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, filepath.Base(file))
	}
	sort.Strings(names)
	var right, missed, invented, reqRight, reqMissed, reqInvented, expected int
	for _, name := range names {
		want, ok := fixtures[name]
		if !ok {
			t.Errorf("%s has no expectation", name)
			continue
		}
		text, err := os.ReadFile(filepath.Join("testdata/readmes", name))
		if err != nil {
			t.Fatal(err)
		}
		got := parseRequirements(string(text), want.repo)
		wantEnv, wantReq := setOf(want.env), setOf(want.required)
		gotEnv, gotReq := map[string]bool{}, map[string]bool{}
		for _, e := range got.Env {
			gotEnv[e.Name] = true
			if e.Required {
				gotReq[e.Name] = true
			}
			if e.AllowEmpty && e.Required {
				t.Errorf("%s: %s allows an empty password and is required", name, e.Name)
			}
			if e.Secret && e.Example != "" {
				t.Errorf("%s: secret %s carries an example", name, e.Name)
			}
		}
		var r, m, i, rr, rm, ri []string
		for n := range wantEnv {
			if gotEnv[n] {
				r = append(r, n)
			} else {
				m = append(m, n)
			}
		}
		for n := range gotEnv {
			if !wantEnv[n] {
				i = append(i, n)
			}
		}
		for n := range wantReq {
			if gotReq[n] {
				rr = append(rr, n)
			} else {
				rm = append(rm, n)
			}
		}
		for n := range gotReq {
			if !wantReq[n] {
				ri = append(ri, n)
			}
		}
		sort.Strings(m)
		sort.Strings(i)
		sort.Strings(ri)
		right, missed, invented, expected = right+len(r), missed+len(m), invented+len(i), expected+len(want.env)
		reqRight, reqMissed, reqInvented = reqRight+len(rr), reqMissed+len(rm), reqInvented+len(ri)
		t.Logf("%-28s expected %2d found right %2d missed %2d invented %d | required right %d/%d invented %d | volumes %v ports %v%s",
			name, len(want.env), len(r), len(m), len(i), len(rr), len(wantReq), len(ri), got.Volumes, got.Ports, func() string {
				out := ""
				if len(m) > 0 {
					out += fmt.Sprintf("\n    missed %v", m)
				}
				if len(i) > 0 {
					out += fmt.Sprintf("\n    invented %v", i)
				}
				return out
			}())
		if len(ri) > 0 {
			t.Errorf("%s: invented required %v", name, ri)
		}
		if len(i) > 0 {
			t.Errorf("%s: invented %v", name, i)
		}
		for _, v := range want.volumes {
			if !setOf(got.Volumes)[v] {
				t.Errorf("%s: volume %s not found in %v", name, v, got.Volumes)
			}
		}
		for _, p := range want.ports {
			if !setOf(got.Ports)[p] {
				t.Errorf("%s: port %s not found in %v", name, p, got.Ports)
			}
		}
	}
	t.Logf("TOTAL %d fixtures: expected %d, found right %d, missed %d, invented %d; required right %d, missed %d, invented %d",
		len(names), expected, right, missed, invented, reqRight, reqMissed, reqInvented)
	if right*100 < expected*80 {
		t.Errorf("recall fell under 80%%: %d of %d", right, expected)
	}
}

// What the official images say about their variables, read in detail.
func TestOfficialImageDetails(t *testing.T) {
	text, _ := os.ReadFile("testdata/readmes/library_mysql.md")
	got := parseRequirements(string(text), "library/mysql")
	byName := map[string]HintEnv{}
	for _, e := range got.Env {
		byName[e.Name] = e
	}
	root := byName["MYSQL_ROOT_PASSWORD"]
	if !root.Required || !root.Secret || root.Example != "" || got.Env[0].Name != "MYSQL_ROOT_PASSWORD" {
		t.Fatalf("%+v first %s", root, got.Env[0].Name)
	}
	if e := byName["MYSQL_ALLOW_EMPTY_PASSWORD"]; !e.AllowEmpty || e.Required {
		t.Fatalf("%+v", e)
	}
	if e := byName["MYSQL_DATABASE"]; e.Required || !e.Optional || !strings.Contains(e.About, "optional") {
		t.Fatalf("%+v", e)
	}
	text, _ = os.ReadFile("testdata/readmes/linuxserver_mariadb.md")
	got = parseRequirements(string(text), "linuxserver/mariadb")
	byName = map[string]HintEnv{}
	for _, e := range got.Env {
		byName[e.Name] = e
	}
	if e := byName["PUID"]; e.Example != "1000" || e.Required || e.Optional || !strings.Contains(e.About, "UserID") {
		t.Fatalf("%+v", e)
	}
	if e := byName["MYSQL_DATABASE"]; !e.Optional || e.Example != "USER_DB_NAME" {
		t.Fatalf("%+v", e)
	}
	text, _ = os.ReadFile("testdata/readmes/bitnami_mariadb.md")
	got = parseRequirements(string(text), "bitnami/mariadb")
	for _, e := range got.Env {
		if e.Name == "MARIADB_CHARACTER_SET" && e.Default != "utf8" {
			t.Fatalf("%+v", e)
		}
	}
}

// ---- the parser's rules and bounds ----

func TestRunExamplesOfThisImageOnly(t *testing.T) {
	text := "```\n" +
		"docker run -d --name app -e APP_KEY_ID=1 -e OTHER=2 other/image\n" +
		"docker run -d --network n --name some \\\n  --env=FIRST=a \\\n  --env SECOND=\"b c\" \\\n  -v data:/var/lib/app:Z -p 127.0.0.1:8080:80 -p 53:53/udp \\\n  team/app:1.2 serve --flag -e NOT_THIS=1\n" +
		"docker run --rm busybox -e BUSY=1\n" +
		"```\n"
	got := parseRequirements(text, "team/app")
	names := []string{}
	for _, e := range got.Env {
		names = append(names, e.Name+"="+e.Example)
	}
	if strings.Join(names, ",") != "FIRST=a,SECOND=b c" {
		t.Fatalf("%v", names)
	}
	if strings.Join(got.Volumes, ",") != "/var/lib/app" || strings.Join(got.Ports, ",") != "80/tcp,53/udp" {
		t.Fatalf("%v %v", got.Volumes, got.Ports)
	}
	// single files are not folders to keep
	got = parseRequirements("docker run -v ./my.cnf:/etc/app/my.cnf:ro -v /var/run/docker.sock:/var/run/docker.sock -v d:/home/node/.n8n team/app\n", "team/app")
	if strings.Join(got.Volumes, ",") != "/home/node/.n8n" {
		t.Fatalf("%v %v", got.Volumes, got.Ports)
	}
}

func TestRequiredOnlyWhenEveryRunSetsIt(t *testing.T) {
	text := "```\n" +
		"docker run -e DB_PASSWORD=x -e DB_USER=u team/app\n" +
		"docker run -e DB_PASSWORD_FILE=/run/secrets/p team/app\n" +
		"docker run -e DB_PASSWORD=y -e ALLOW_EMPTY_PASSWORD=yes team/app\n" +
		"```\n"
	got := parseRequirements(text, "team/app")
	req := map[string]bool{}
	for _, e := range got.Env {
		req[e.Name] = e.Required
	}
	if !req["DB_PASSWORD"] || req["DB_USER"] || req["ALLOW_EMPTY_PASSWORD"] {
		t.Fatalf("%+v", got.Env)
	}
	// one example alone proves nothing
	got = parseRequirements("docker run -e DB_PASSWORD=x team/app\n", "team/app")
	if len(got.Env) != 1 || got.Env[0].Required {
		t.Fatalf("%+v", got.Env)
	}
	// "required" with a condition is not required
	text = "## Environment variables\n\n" +
		"- `A_ONE`: required.\n" +
		"- `A_TWO`: is required if you use LDAP.\n" +
		"- `A_THREE`: Not required for AWS.\n" +
		"- `A_FOUR`: optional, but required for a user to be created.\n" +
		"- `A_FIVE`: This variable is mandatory.\n" +
		"- `A_SIX`: The method. Use `PLAIN` if no authentication is required.\n" +
		"- `A_SEVEN`: A key. Length over 5. Required for all nodes.\n"
	got = parseRequirements(text, "team/app")
	req = map[string]bool{}
	for _, e := range got.Env {
		req[e.Name] = e.Required
	}
	if !req["A_ONE"] || req["A_TWO"] || req["A_THREE"] || req["A_FOUR"] || !req["A_FIVE"] || req["A_SIX"] || req["A_SEVEN"] || len(got.Env) != 7 {
		t.Fatalf("%+v", got.Env)
	}
}

func TestComposeServiceOfThisImageOnly(t *testing.T) {
	text := "```yaml\nservices:\n  db:\n    image: postgres:18\n    environment:\n      POSTGRES_PASSWORD: example\n" +
		"  app:\n    image: \"team/app:2\"\n\n    environment:\n      # a comment\n      - APP_MODE=prod # optional\n      - \"APP_NAME=a b\"\n" +
		"    volumes:\n      - ./data:/data\n      - type: bind\n    ports:\n      - \"8080:80\"\n      #- \"9000:9000\"\n" +
		"  other:\n    image: other\n    environment:\n      - OTHER=1\n```\n"
	got := parseRequirements(text, "team/app")
	names := []string{}
	for _, e := range got.Env {
		names = append(names, fmt.Sprintf("%s=%s/%v", e.Name, e.Example, e.Optional))
	}
	if strings.Join(names, ",") != "APP_MODE=prod/true,APP_NAME=a b/false" {
		t.Fatalf("%v", names)
	}
	if strings.Join(got.Volumes, ",") != "/data" || strings.Join(got.Ports, ",") != "80/tcp" {
		t.Fatalf("%v %v", got.Volumes, got.Ports)
	}
}

func TestGrammarAndNonVariables(t *testing.T) {
	text := "## Environment variables\n\n| Name | Default | Description |\n|---|---|---|\n" +
		"| `GOOD_ONE` | `1` | fine |\n| `lower_case` | x | no |\n| `9STARTS` | x | no |\n| `HTTP` | x | no |\n" +
		"| `" + strings.Repeat("A", 65) + "` | x | too long |\n| `A-DASH` | x | no |\n| `PUID` | `911` | yes |\n| `_` | x | no |\n"
	got := parseRequirements(text, "team/app")
	names := []string{}
	for _, e := range got.Env {
		names = append(names, e.Name+":"+e.Default)
	}
	if strings.Join(names, ",") != "GOOD_ONE:1,PUID:911" {
		t.Fatalf("%v", names)
	}
	// a read-only table is not a list of settings
	got = parseRequirements("#### Read-only environment variables\n\n| Name | Value |\n|---|---|\n| `APP_BASE_DIR` | `/opt` |\n", "team/app")
	if len(got.Env) != 0 {
		t.Fatalf("%+v", got.Env)
	}
	// file variants are not settings of their own
	got = parseRequirements("docker run -e A_PASSWORD_FILE=/run/s -e FILE__B=/run/b -e A_PASSWORD=x -e TLS_CERT_FILE=/c team/app\n", "team/app")
	if len(got.Env) != 2 || got.Env[0].Name != "A_PASSWORD" || got.Env[1].Name != "TLS_CERT_FILE" {
		t.Fatalf("%+v", got.Env)
	}
}

func TestSecretMirrorsThePanel(t *testing.T) {
	// the cases of client/src/utils/anyImage.js isSecretName
	for name, secret := range map[string]bool{
		"POSTGRES_PASSWORD": true, "DB_PASS": true, "MY_PASSWD": true, "APP_SECRET": true, "GH_TOKEN": true, "API_KEY": true,
		"KEY": true, "PRIVATE_KEY_PATH": true, "AWS_CREDENTIALS": true, "POSTGRES_PASSWORD_FILE": false, "KEYBOARD": false,
		"MONKEY": false, "PASSFILE_PATH": false, "TZ": false, "PUID": false,
	} {
		if isSecretEnv(name) != secret {
			t.Errorf("%s: %v", name, !secret)
		}
	}
}

func TestControlCharactersAndMarkup(t *testing.T) {
	text := "## Environment variables\n\n" +
		"- `A_VAR`: has \x1b[31mred\x1b[0m and \u202eevil\u202c <script>alert(1)</script> [a link](javascript:alert(1)) **bold**. Default: `x\x07y`\n" +
		"```\ndocker run -e B_VAR=\"<img src=x onerror=alert(1)>\x00v\" team/app\n```\n"
	got := parseRequirements(text, "team/app")
	if len(got.Env) != 2 {
		t.Fatalf("%+v", got.Env)
	}
	for _, e := range got.Env {
		for _, s := range []string{e.Name, e.About, e.Default, e.Example} {
			for _, r := range s {
				if r < 0x20 || r == 0x7f || (r >= 0x202a && r <= 0x202e) {
					t.Fatalf("control character in %q", s)
				}
			}
			if strings.ContainsAny(s, "<>") || strings.Contains(s, "javascript") {
				t.Fatalf("markup in %q", s)
			}
		}
	}
	if got.Env[1].Name != "A_VAR" || got.Env[1].Default != "xy" || !strings.Contains(got.Env[1].About, "a link") {
		t.Fatalf("%+v", got.Env[1])
	}
}

func TestBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString("## Environment variables\n\n| Name | Description |\n|---|---|\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "| `VAR_%03d` | %s |\n", i, strings.Repeat("words ", 20))
	}
	b.WriteString("```\ndocker run")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, " -v /v%03d:/data%03d -p %d:%d", i, i, 1000+i, 1000+i)
	}
	b.WriteString(" -e LONG_EXAMPLE=" + strings.Repeat("x", 1000) + " team/app\n```\n")
	got := parseRequirements(b.String(), "team/app")
	if len(got.Env) != maxHintEnv || len(got.Volumes) != maxHintVolumes || len(got.Ports) != maxHintPorts || !got.Truncated {
		t.Fatalf("%d %d %d %v", len(got.Env), len(got.Volumes), len(got.Ports), got.Truncated)
	}
	// what the example sets comes before what the table lists
	if got.Env[0].Name != "LONG_EXAMPLE" || len(got.Env[0].Example) > hintExampleLen {
		t.Fatalf("%s %d", got.Env[0].Name, len(got.Env[0].Example))
	}
	for _, e := range got.Env {
		if len(e.About) > hintAboutLen || len(e.Name) > 64 {
			t.Fatal(len(e.About))
		}
	}
}

func TestHostileDescription(t *testing.T) {
	var b strings.Builder
	// huge, deeply nested markdown, HTML and script, unclosed fences and code spans
	for i := 0; i < 20000; i++ {
		b.WriteString(strings.Repeat("> ", 50) + strings.Repeat("- [", 20) + "`" + strings.Repeat("*", 30) + "<div><script>x()</script>\n")
	}
	b.WriteString("```\n" + strings.Repeat("docker run "+strings.Repeat("-e A=1 ", 300)+"\\\n", 3000))
	b.WriteString(strings.Repeat("|", 100000) + "\n" + strings.Repeat("`", 50000))
	start := time.Now()
	got := parseRequirements(b.String(), "team/app")
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if !got.Truncated || len(got.Env) > maxHintEnv {
		t.Fatalf("%v %d", got.Truncated, len(got.Env))
	}
	raw, _ := json.Marshal(got)
	if strings.ContainsAny(string(raw), "<>") || strings.Contains(string(raw), "x()") {
		t.Fatal(string(raw))
	}
	// shell words never run anything: the command is only split
	words := shellWords(`-e A="$(rm -rf /)" -e B='x' ; rm -rf / | sh`)
	if strings.Join(words, "|") != "-e|A=$(rm -rf /)|-e|B=x" {
		t.Fatalf("%q", words)
	}
}

func TestImageMatches(t *testing.T) {
	for _, c := range []struct {
		word, repo string
		ok         bool
	}{
		{"postgres", "library/postgres", true}, {"postgres:18-alpine", "library/postgres", true},
		{"docker.io/library/postgres@sha256:abc", "library/postgres", true}, {"bitnami/postgresql", "library/postgres", false},
		{"some-postgres", "library/postgres", false}, {"REGISTRY_NAME/bitnami/redis:latest", "bitnami/redis", true},
		{"lscr.io/linuxserver/sonarr:latest", "linuxserver/sonarr", true}, {"docker.n8n.io/n8nio/n8n", "n8nio/n8n", true},
		{"mongo-express", "library/mongo", false}, {"redis", "bitnami/redis", false}, {"", "library/redis", false},
	} {
		if imageMatches(c.word, c.repo) != c.ok {
			t.Errorf("%s %s", c.word, c.repo)
		}
	}
}

func TestDocsFromLabels(t *testing.T) {
	docs := docsFrom(map[string]string{
		"org.opencontainers.image.documentation": "https://docs.example.org/app",
		"org.opencontainers.image.url":           "http://example.org",
		"org.opencontainers.image.source":        "https://user:pw@github.com/team/app",
	})
	if strings.Join(docs, ",") != "https://docs.example.org/app" {
		t.Fatal(docs)
	}
	docs = docsFrom(map[string]string{"org.opencontainers.image.source": "https://github.com/team/app", "org.opencontainers.image.url": "https://github.com/team/app"})
	if strings.Join(docs, ",") != "https://github.com/team/app" {
		t.Fatal(docs)
	}
	for _, bad := range []string{"javascript:alert(1)", "https://", "https://a b", "https://x/<script>", "ftp://x", strings.Repeat("https://x/", 100)} {
		if len(docsFrom(map[string]string{"org.opencontainers.image.url": bad})) != 0 {
			t.Error(bad)
		}
	}
	if h := withDocs(nil, &Declared{Labels: map[string]string{"org.opencontainers.image.url": "https://example.org"}}); h == nil || h.Source != "labels" || len(h.Env) != 0 {
		t.Fatalf("%+v", h)
	}
	if withDocs(nil, nil) != nil {
		t.Fatal("nothing to say")
	}
}

// ---- reading: the guarded client, the cache, the bounds, the kill switch ----

type hubReads struct {
	mu    sync.Mutex
	paths []string
}

func (h *hubReads) count() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.paths) }

func standInHub(t *testing.T, status int, body string) *hubReads {
	reads := &hubReads{}
	fakeHub(t, func(w http.ResponseWriter, r *http.Request) {
		reads.mu.Lock()
		reads.paths = append(reads.paths, r.URL.RequestURI())
		reads.mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			t.Errorf("credentials sent to the API: %s", r.URL)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
	oldQuay := quayAPI
	quayAPI = hubAPI
	readmes.clear()
	readmePauseMu.Lock()
	readmePaused = time.Time{}
	readmePauseMu.Unlock()
	t.Cleanup(func() { quayAPI = oldQuay; readmes.clear() })
	return reads
}

func hubDescription(text string) string {
	raw, _ := json.Marshal(map[string]interface{}{"name": "app", "full_description": text, "description": text})
	return string(raw)
}

func TestDescriptionReadAndCached(t *testing.T) {
	reads := standInHub(t, 200, hubDescription("docker run -e APP_PASSWORD=x -p 80:80 team/app\n"))
	ref := Reference{Registry: hubRegistry, Repository: "team/app", Tag: "1"}
	hints := descriptionHints(context.Background(), ref, true)
	if hints == nil || hints.Source != "description" || len(hints.Env) != 1 || hints.Env[0].Name != "APP_PASSWORD" || !hints.Env[0].Secret {
		t.Fatalf("%+v", hints)
	}
	if reads.paths[0] != "/v2/namespaces/team/repositories/app" {
		t.Fatal(reads.paths)
	}
	// another tag, same repository: from the cache
	ref.Tag = "2"
	if h := descriptionHints(context.Background(), ref, true); h == nil || reads.count() != 1 {
		t.Fatalf("%d reads", reads.count())
	}
	// linuxserver's own registry reads the same images' description on Docker Hub
	descriptionHints(context.Background(), Reference{Registry: "lscr.io", Repository: "linuxserver/sonarr", Tag: "latest"}, true)
	if reads.paths[1] != "/v2/namespaces/linuxserver/repositories/sonarr" {
		t.Fatal(reads.paths)
	}
	// Quay
	descriptionHints(context.Background(), Reference{Registry: "quay.io", Repository: "team/tool", Tag: "1"}, true)
	if reads.paths[2] != "/api/v1/repository/team/tool?includeTags=false" {
		t.Fatal(reads.paths)
	}
	// GHCR and other registries have no description to read
	descriptionHints(context.Background(), Reference{Registry: "ghcr.io", Repository: "team/app", Tag: "1"}, true)
	descriptionHints(context.Background(), Reference{Registry: "registry.example.org", Repository: "team/app", Tag: "1"}, true)
	if reads.count() != 3 {
		t.Fatal(reads.paths)
	}
}

func TestDescriptionNotReadWhenNotAllowed(t *testing.T) {
	reads := standInHub(t, 200, hubDescription("docker run -e APP_X=1 team/app\n"))
	ref := Reference{Registry: hubRegistry, Repository: "team/app", Tag: "1"}
	if descriptionHints(context.Background(), ref, false) != nil || reads.count() != 0 {
		t.Fatal("a recipe's image must not cost a read")
	}
	descriptionHints(context.Background(), ref, true)
	if h := descriptionHints(context.Background(), ref, false); h == nil || reads.count() != 1 {
		t.Fatal("but the cache may answer")
	}
}

func TestDescriptionKillSwitch(t *testing.T) {
	reads := standInHub(t, 200, hubDescription("docker run -e APP_X=1 team/app\n"))
	descriptionsDisabled = true
	defer func() { descriptionsDisabled = false }()
	if descriptionHints(context.Background(), Reference{Registry: hubRegistry, Repository: "team/app", Tag: "1"}, true) != nil || reads.count() != 0 {
		t.Fatal("the kill switch must stop the read before it starts")
	}
}

func TestDescriptionBoundedAndFailures(t *testing.T) {
	// an answer over the bound is refused, and remembered for a while
	reads := standInHub(t, 200, hubDescription(strings.Repeat("x", readmeAnswerLimit)))
	ref := Reference{Registry: hubRegistry, Repository: "team/app", Tag: "1"}
	if descriptionHints(context.Background(), ref, true) != nil {
		t.Fatal("too large")
	}
	descriptionHints(context.Background(), ref, true)
	if reads.count() != 1 {
		t.Fatal("a failure is cached", reads.count())
	}
	// 429: every description read waits
	reads = standInHub(t, 429, "{}")
	descriptionHints(context.Background(), ref, true)
	descriptionHints(context.Background(), Reference{Registry: hubRegistry, Repository: "team/other", Tag: "1"}, true)
	if reads.count() != 1 {
		t.Fatal("after a 429 no more reads", reads.count())
	}
	// not JSON
	reads = standInHub(t, 200, "<html>")
	if descriptionHints(context.Background(), ref, true) != nil {
		t.Fatal("not JSON")
	}
}

// The description is read through the same guarded client: an API on an
// address the guard refuses (here the loopback stand-in with the real check)
// is never reached.
func TestDescriptionReadGoesThroughTheGuard(t *testing.T) {
	reads := standInHub(t, 200, hubDescription("docker run -e APP_X=1 team/app\n"))
	checkAddr = refusedAddr
	if descriptionHints(context.Background(), Reference{Registry: hubRegistry, Repository: "team/app", Tag: "1"}, true) != nil || reads.count() != 0 {
		t.Fatalf("the guard let the read through: %v", reads.paths)
	}
}

// Describe fills Hints for an image without a recipe, from the publisher's description.
func TestDescribeFillsHints(t *testing.T) {
	f := newFakeRegistry(t, nil)
	reads := &hubReads{}
	f.hubAPIHandler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/namespaces/team/repositories/app" {
			return false
		}
		reads.mu.Lock()
		reads.paths = append(reads.paths, r.URL.Path)
		reads.mu.Unlock()
		w.Write([]byte(hubDescription("## Environment variables\n\n- `APP_SECRET_KEY`: This variable is required.\n")))
		return true
	}
	hubRegistryHost, hubAPI = f.host(), f.main.URL
	defer func() { hubRegistryHost, hubAPI = "registry-1.docker.io", "https://hub.docker.com" }()
	readmes.clear()
	defer readmes.clear()
	serverPlatform = func() Platform { return amd64 }
	desc := Describe(context.Background(), "team/app:18", "user:a")
	if desc.Hints == nil || len(desc.Hints.Env) != 1 || !desc.Hints.Env[0].Required || reads.count() != 1 {
		t.Fatalf("%q %+v", desc.Problem, desc.Hints)
	}
}

// The real Docker Hub, opt-in (one call of its API, not a pull):
//
//	ANYIMAGE_LIVE_HUB=1 go test ./src/anyimage/ -run DescriptionLive -v
func TestDescriptionLive(t *testing.T) {
	if os.Getenv("ANYIMAGE_LIVE_HUB") != "1" {
		t.Skip("set ANYIMAGE_LIVE_HUB=1 to ask the real Docker Hub")
	}
	readmes.clear()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hints := descriptionHints(ctx, Reference{Registry: hubRegistry, Repository: "library/postgres", Tag: "18"}, true)
	if hints == nil || len(hints.Env) == 0 {
		t.Fatalf("%+v", hints)
	}
	t.Logf("postgres: %d variables, first %+v", len(hints.Env), hints.Env[0])
	if hints.Env[0].Name != "POSTGRES_PASSWORD" || !hints.Env[0].Required || !hints.Env[0].Secret {
		t.Fatalf("%+v", hints.Env[0])
	}
}
