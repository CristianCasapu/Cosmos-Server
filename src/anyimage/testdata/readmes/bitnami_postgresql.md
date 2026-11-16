<!-- source: https://hub.docker.com/r/bitnami/postgresql fetched 2026-11-15, extract -->

```console
docker run --name postgresql REGISTRY_NAME/bitnami/postgresql:latest
```

> Note: You need to substitute the `REGISTRY_NAME` placeholder with a reference to your container registry.

The following section describes the supported environment variables

| Name                                       | Description                                                                                          | Default Value                           |
|--------------------------------------------|------------------------------------------------------------------------------------------------------|-----------------------------------------|
| `POSTGRESQL_VOLUME_DIR`                    | Persistence base directory                                                                           | `/bitnami/postgresql`                   |
| `POSTGRESQL_DATA_DIR`                      | PostgreSQL data directory                                                                            | `${POSTGRESQL_VOLUME_DIR}/data`         |
| `POSTGRESQL_EXTRA_FLAGS`                   | Extra flags for PostgreSQL initialization                                                            | `nil`                                   |
| `POSTGRESQL_INIT_MAX_TIMEOUT`              | Maximum initialization waiting timeout                                                               | `60`                                    |
| `POSTGRESQL_PGCTLTIMEOUT`                  | Maximum waiting timeout for pg_ctl commands                                                          | `60`                                    |
| `POSTGRESQL_SHUTDOWN_MODE`                 | Default mode for pg_ctl stop command                                                                 | `fast`                                  |
| `POSTGRESQL_CLUSTER_APP_NAME`              | Replication cluster default application name                                                         | `walreceiver`                           |
| `POSTGRESQL_DATABASE`                      | Default PostgreSQL database                                                                          | `postgres`                              |
| `POSTGRESQL_INITDB_ARGS`                   | Optional args for PostreSQL initdb operation                                                         | `nil`                                   |
| `ALLOW_EMPTY_PASSWORD`                     | Allow password-less access                                                                           | `no`                                    |
| `POSTGRESQL_INITDB_WAL_DIR`                | Optional init db wal directory                                                                       | `nil`                                   |
| `POSTGRESQL_MASTER_HOST`                   | PostgreSQL master host (used by slaves)                                                              | `nil`                                   |
| `POSTGRESQL_MASTER_PORT_NUMBER`            | PostgreSQL master host port (used by slaves)                                                         | `5432`                                  |
| `POSTGRESQL_NUM_SYNCHRONOUS_REPLICAS`      | Number of PostgreSQL replicas that should use synchronous replication                                | `0`                                     |
| `POSTGRESQL_SYNCHRONOUS_REPLICAS_MODE`     | PostgreSQL synchronous replication mode (values: empty, FIRST, ANY)                                  | `nil`                                   |
| `POSTGRESQL_PORT_NUMBER`                   | PostgreSQL port number                                                                               | `5432`                                  |
| `POSTGRESQL_ALLOW_REMOTE_CONNECTIONS`      | Modify pg_hba settings so users can access from the outside                                          | `yes`                                   |
| `POSTGRESQL_REPLICATION_MODE`              | PostgreSQL replication mode (values: master, slave)                                                  | `master`                                |
| `POSTGRESQL_REPLICATION_USER`              | PostgreSQL replication user                                                                          | `nil`                                   |
| `POSTGRESQL_REPLICATION_USE_PASSFILE`      | Use PGPASSFILE instead of PGPASSWORD                                                                 | `yes`                                   |
| `POSTGRESQL_REPLICATION_PASSFILE_PATH`     | Path to store passfile                                                                               | `${POSTGRESQL_CONF_DIR}/.pgpass`        |
| `POSTGRESQL_SR_CHECK`                      | Create user on PostgreSQL for Stream Replication Check                                               | `no`                                    |
| `POSTGRESQL_SR_CHECK_USERNAME`             | Stream Replication Check user                                                                        | `sr_check_user`                         |
| `POSTGRESQL_SR_CHECK_DATABASE`             | Stream Replication Check database                                                                    | `postgres`                              |
| `POSTGRESQL_SYNCHRONOUS_COMMIT_MODE`       | Enable synchronous replication in slaves (number defined by POSTGRESQL_NUM_SYNCHRONOUS_REPLICAS)     | `on`                                    |
| `POSTGRESQL_FSYNC`                         | Enable fsync in write ahead logs                                                                     | `on`                                    |
| `POSTGRESQL_USERNAME`                      | PostgreSQL default username                                                                          | `postgres`                              |
| `POSTGRESQL_ENABLE_LDAP`                   | Enable LDAP for PostgreSQL authentication                                                            | `no`                                    |
| `POSTGRESQL_LDAP_URL`                      | PostgreSQL LDAP server url (requires POSTGRESQL_ENABLE_LDAP=yes)                                     | `nil`                                   |
| `POSTGRESQL_LDAP_PREFIX`                   | PostgreSQL LDAP prefix (requires POSTGRESQL_ENABLE_LDAP=yes)                                         | `nil`                                   |
| `POSTGRESQL_LDAP_SUFFIX`                   | PostgreSQL LDAP suffix (requires POSTGRESQL_ENABLE_LDAP=yes)                                         | `nil`                                   |
| `POSTGRESQL_LDAP_SERVER`                   | PostgreSQL LDAP server (requires POSTGRESQL_ENABLE_LDAP=yes)                                         | `nil`                                   |
| `POSTGRESQL_LDAP_PORT`                     | PostgreSQL LDAP port (requires POSTGRESQL_ENABLE_LDAP=yes)                                           | `nil`                                   |
| `POSTGRESQL_LDAP_SCHEME`                   | PostgreSQL LDAP scheme (requires POSTGRESQL_ENABLE_LDAP=yes)                                         | `nil`                                   |
| `POSTGRESQL_LDAP_TLS`                      | PostgreSQL LDAP tls setting (requires POSTGRESQL_ENABLE_LDAP=yes)                                    | `nil`                                   |
| `POSTGRESQL_LDAP_BASE_DN`                  | PostgreSQL LDAP base DN settings (requires POSTGRESQL_ENABLE_LDAP=yes)                               | `nil`                                   |
| `POSTGRESQL_LDAP_BIND_DN`                  | PostgreSQL LDAP bind DN settings (requires POSTGRESQL_ENABLE_LDAP=yes)                               | `nil`                                   |
| `POSTGRESQL_LDAP_BIND_PASSWORD`            | PostgreSQL LDAP bind password (requires POSTGRESQL_ENABLE_LDAP=yes)                                  | `nil`                                   |
| `POSTGRESQL_LDAP_BIND_USE_PASSFILE`        | Use a passfile for LDAP bind password                                                                | `yes`                                   |
| `POSTGRESQL_LDAP_BIND_PASSFILE_PATH`       | Path to store LDAP bind password passfile                                                            | `${POSTGRESQL_CONF_DIR}/.ldap_bindpass` |
| `POSTGRESQL_LDAP_SEARCH_ATTR`              | PostgreSQL LDAP search attribute (requires POSTGRESQL_ENABLE_LDAP=yes)                               | `nil`                                   |
| `POSTGRESQL_LDAP_SEARCH_FILTER`            | PostgreSQL LDAP search filter (requires POSTGRESQL_ENABLE_LDAP=yes)                                  | `nil`                                   |
| `POSTGRESQL_INITSCRIPTS_USERNAME`          | Username for the psql scripts included in /docker-entrypoint.initdb                                  | `$POSTGRESQL_USERNAME`                  |
| `POSTGRESQL_PASSWORD`                      | Password for the PostgreSQL created user                                                             | `nil`                                   |
| `POSTGRESQL_POSTGRES_PASSWORD`             | Password for the PostgreSQL postgres user                                                            | `nil`                                   |
| `POSTGRESQL_REPLICATION_PASSWORD`          | Password for the PostgreSQL replication user                                                         | `nil`                                   |
| `POSTGRESQL_SR_CHECK_PASSWORD`             | Password for the Stream Replication Check user                                                       | `nil`                                   |
| `POSTGRESQL_INITSCRIPTS_PASSWORD`          | Password for the PostgreSQL init scripts user                                                        | `$POSTGRESQL_PASSWORD`                  |
| `POSTGRESQL_ENABLE_TLS`                    | Whether to enable TLS for traffic or not                                                             | `no`                                    |
| `POSTGRESQL_TLS_CERT_FILE`                 | File containing the certificate for the TLS traffic                                                  | `nil`                                   |
| `POSTGRESQL_TLS_KEY_FILE`                  | File containing the key for certificate                                                              | `nil`                                   |
| `POSTGRESQL_TLS_CA_FILE`                   | File containing the CA of the certificate                                                            | `nil`                                   |
| `POSTGRESQL_TLS_CRL_FILE`                  | File containing a Certificate Revocation List                                                        | `nil`                                   |
| `POSTGRESQL_TLS_PREFER_SERVER_CIPHERS`     | Whether to use the server TLS cipher preferences rather than the client                              | `yes`                                   |
| `POSTGRESQL_SHARED_PRELOAD_LIBRARIES`      | List of libraries to preload at PostgreSQL initialization                                            | `pgaudit`                               |
| `POSTGRESQL_PGAUDIT_LOG`                   | Comma-separated list of actions to log with pgaudit                                                  | `nil`                                   |
| `POSTGRESQL_PGAUDIT_LOG_CATALOG`           | Enable pgaudit log catalog (pgaudit.log_catalog setting)                                             | `nil`                                   |
| `POSTGRESQL_PGAUDIT_LOG_PARAMETER`         | Enable pgaudit log parameter (pgaudit.log_parameter setting)                                         | `nil`                                   |
| `POSTGRESQL_LOG_CONNECTIONS`               | Add a log entry per user connection                                                                  | `nil`                                   |
| `POSTGRESQL_LOG_DISCONNECTIONS`            | Add a log entry per user disconnection                                                               | `nil`                                   |
| `POSTGRESQL_LOG_HOSTNAME`                  | Log the client host name when accessing                                                              | `nil`                                   |
| `POSTGRESQL_CLIENT_MIN_MESSAGES`           | Set log level of errors to send to the client                                                        | `error`                                 |
| `POSTGRESQL_LOG_LINE_PREFIX`               | Set the format of the log lines                                                                      | `nil`                                   |
| `POSTGRESQL_LOG_TIMEZONE`                  | Set the log timezone                                                                                 | `nil`                                   |
| `POSTGRESQL_TIMEZONE`                      | Set the timezone                                                                                     | `nil`                                   |
| `POSTGRESQL_MAX_CONNECTIONS`               | Set the maximum amount of connections                                                                | `nil`                                   |
| `POSTGRESQL_TCP_KEEPALIVES_IDLE`           | Set the TCP keepalive idle time                                                                      | `nil`                                   |
| `POSTGRESQL_TCP_KEEPALIVES_INTERVAL`       | Set the TCP keepalive interval time                                                                  | `nil`                                   |
| `POSTGRESQL_TCP_KEEPALIVES_COUNT`          | Set the TCP keepalive count                                                                          | `nil`                                   |
| `POSTGRESQL_STATEMENT_TIMEOUT`             | Set the SQL statement timeout                                                                        | `nil`                                   |
| `POSTGRESQL_PGHBA_AUTH_METHOD`             | Auth method to use for pg_hba.conf                                                                   | `md5`                                   |
| `POSTGRESQL_PGHBA_REMOVE_FILTERS`          | Comma-separated list of strings for removing pg_hba.conf lines (example: md5, local)                 | `nil`                                   |
| `POSTGRESQL_USERNAME_CONNECTION_LIMIT`     | Set the user connection limit                                                                        | `nil`                                   |
| `POSTGRESQL_POSTGRES_CONNECTION_LIMIT`     | Set the postgres user connection limit                                                               | `nil`                                   |
| `POSTGRESQL_WAL_LEVEL`                     | Set the write-ahead log level                                                                        | `replica`                               |
| `POSTGRESQL_DEFAULT_TOAST_COMPRESSION`     | Set the postgres default compression                                                                 | `nil`                                   |
| `POSTGRESQL_PASSWORD_ENCRYPTION`           | Set the passwords encryption method                                                                  | `nil`                                   |
| `POSTGRESQL_DEFAULT_TRANSACTION_ISOLATION` | Set transaction isolation                                                                            | `nil`                                   |
| `POSTGRESQL_REPLICATION_NODES`             | Override value for synchronous_standby_names in postgresql.conf. Should be set if `REPMGR_NODE_NAME` | `nil`                                   |
| `POSTGRESQL_PERFORM_RESTORE`               | Flag to skip deletion of `recovery.signal` file to enable native recovery. e.g by using `wal-g`      | `no`                                    |

| `POSTGRESQL_BASE_DIR`                        | PostgreSQL installation directory                               | `/opt/bitnami/postgresql`                     |
| `POSTGRESQL_DEFAULT_CONF_DIR`                | PostgreSQL configuration directory                              | `$POSTGRESQL_BASE_DIR/conf.default`           |
| `POSTGRESQL_CONF_DIR`                        | PostgreSQL configuration directory                              | `$POSTGRESQL_BASE_DIR/conf`                   |
| `POSTGRESQL_MOUNTED_CONF_DIR`                | PostgreSQL mounted configuration directory                      | `$POSTGRESQL_VOLUME_DIR/conf`                 |
| `POSTGRESQL_CONF_FILE`                       | PostgreSQL configuration file                                   | `$POSTGRESQL_CONF_DIR/postgresql.conf`        |
| `POSTGRESQL_PGHBA_FILE`                      | PostgreSQL pg_hba file                                          | `$POSTGRESQL_CONF_DIR/pg_hba.conf`            |
| `POSTGRESQL_RECOVERY_FILE`                   | PostgreSQL recovery file                                        | `$POSTGRESQL_DATA_DIR/recovery.conf`          |
| `POSTGRESQL_LOG_DIR`                         | PostgreSQL logs directory                                       | `$POSTGRESQL_BASE_DIR/logs`                   |
| `POSTGRESQL_LOG_FILE`                        | PostgreSQL log file                                             | `$POSTGRESQL_LOG_DIR/postgresql.log`          |
| `POSTGRESQL_TMP_DIR`                         | PostgreSQL temporary directory                                  | `$POSTGRESQL_BASE_DIR/tmp`                    |
| `POSTGRESQL_PID_FILE`                        | PostgreSQL PID file                                             | `$POSTGRESQL_TMP_DIR/postgresql.pid`          |
| `POSTGRESQL_BIN_DIR`                         | PostgreSQL executables directory                                | `$POSTGRESQL_BASE_DIR/bin`                    |
| `POSTGRESQL_INITSCRIPTS_DIR`                 | Init scripts directory                                          | `/docker-entrypoint-initdb.d`                 |
| `POSTGRESQL_PREINITSCRIPTS_DIR`              | Pre-init scripts directory                                      | `/docker-entrypoint-preinitdb.d`              |
| `POSTGRESQL_DAEMON_USER`                     | PostgreSQL system user                                          | `postgres`                                    |
| `POSTGRESQL_DAEMON_GROUP`                    | PostgreSQL system group                                         | `postgres`                                    |
| `POSTGRESQL_USE_CUSTOM_PGHBA_INITIALIZATION` | Initialize PostgreSQL with the custom, mounted pg_hba.conf file | `no`                                          |
| `POSTGRESQL_PGBACKREST_VOLUME_DIR`           | The pgbackrest home directory                                   | `${POSTGRESQL_VOLUME_DIR}/pgbackrest`         |
| `POSTGRESQL_PGBACKREST_LOGS_DIR`             | The pgbackrest logs directory                                   | `${POSTGRESQL_PGBACKREST_VOLUME_DIR}/logs`    |
| `POSTGRESQL_PGBACKREST_BACKUPS_DIR`          | The pgbackrest backups directory                                | `${POSTGRESQL_PGBACKREST_VOLUME_DIR}/backups` |
| `POSTGRESQL_PGBACKREST_SPOOL_DIR`            | The pgbackrest spool directory                                  | `${POSTGRESQL_PGBACKREST_VOLUME_DIR}/spool`   |
| `POSTGRESQL_PGBACKREST_CONF_FILE`            | The pgbackrest configuration file                               | `${POSTGRESQL_DATA_DIR}/pgbackrest.conf`      |
| `POSTGRESQL_FIRST_BOOT`                      | Flag for startup (necessary for repmgr)                         | `yes`                                         |
| `NSS_WRAPPER_LIB`                            | Flag for startup (necessary for repmgr)                         | `/opt/bitnami/common/lib/libnss_wrapper.so`   |

- `POSTGRESQL_EXTRA_FLAGS`: F

