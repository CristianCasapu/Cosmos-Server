<!-- source: https://hub.docker.com/r/bitnami/mongodb fetched 2026-11-15, extract -->

```console
docker run --name mongodb REGISTRY_NAME/bitnami/mongodb:latest
```

> Note: You need to substitute the `REGISTRY_NAME` placeholder with a reference to your container registry.

The following section describes the supported environment variables

| Name                                    | Description                                                                                                                                    | Default Value                       |
|-----------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------|
| `MONGODB_MOUNTED_CONF_DIR`              | Directory for including custom configuration files (that override the default generated ones)                                                  | `${MONGODB_VOLUME_DIR}/conf`        |
| `MONGODB_INIT_RETRY_ATTEMPTS`           | Maximum retries for checking the service initialization status                                                                                 | `7`                                 |
| `MONGODB_INIT_RETRY_DELAY`              | Time (in seconds) to wait between retries for checking the service initialization status                                                       | `5`                                 |
| `MONGODB_PORT_NUMBER`                   | MongoDB port                                                                                                                                   | `$MONGODB_DEFAULT_PORT_NUMBER`      |
| `MONGODB_EXTRA_FLAGS`                   | Extra flags for MongoDB initialization                                                                                                         | `nil`                               |
| `MONGODB_ENABLE_NUMACTL`                | Execute commands using numactl                                                                                                                 | `false`                             |
| `MONGODB_SHELL_EXTRA_FLAGS`             | Extra flags when using the mongodb client during initialization (useful when mounting init scripts)                                            | `nil`                               |
| `MONGODB_ADVERTISED_HOSTNAME`           | Hostname to use for advertising the MongoDB service                                                                                            | `nil`                               |
| `MONGODB_ADVERTISE_IP`                  | Whether advertised hostname is set to container ip                                                                                             | `false`                             |
| `MONGODB_ADVERTISED_PORT_NUMBER`        | MongoDB advertised port number. It is recommended to pass this environment variable if you have a proxy port forwarding requests to container. | `nil`                               |
| `MONGODB_DISABLE_JAVASCRIPT`            | Disable MongoDB server-side javascript execution                                                                                               | `no`                                |
| `MONGODB_ENABLE_JOURNAL`                | Enable MongoDB journal                                                                                                                         | `nil`                               |
| `MONGODB_DISABLE_SYSTEM_LOG`            | Disable MongoDB daemon system log                                                                                                              | `nil`                               |
| `MONGODB_ENABLE_DIRECTORY_PER_DB`       | Use a separate folder for storing each database data                                                                                           | `nil`                               |
| `MONGODB_ENABLE_IPV6`                   | Use IPv6 for database connections                                                                                                              | `nil`                               |
| `MONGODB_SYSTEM_LOG_VERBOSITY`          | MongoDB daemon log level                                                                                                                       | `nil`                               |
| `MONGODB_ROOT_USER`                     | User name for the MongoDB root user                                                                                                            | `root`                              |
| `MONGODB_ROOT_PASSWORD`                 | Password for the MongoDB root user                                                                                                             | `nil`                               |
| `MONGODB_USERNAME`                      | User to generate at initialization time                                                                                                        | `nil`                               |
| `MONGODB_PASSWORD`                      | Password for the non-root user specified in MONGODB_USERNAME                                                                                   | `nil`                               |
| `MONGODB_DATABASE`                      | Name of the database to create at initialization time                                                                                          | `nil`                               |
| `MONGODB_METRICS_USERNAME`              | User used for metrics collection, for example with mongodb_exporter                                                                            | `nil`                               |
| `MONGODB_METRICS_PASSWORD`              | Password for the non-root user specified in MONGODB_METRICS_USERNAME                                                                           | `nil`                               |
| `MONGODB_EXTRA_USERNAMES`               | Comma or semicolon separated list of extra users to be created.                                                                                | `nil`                               |
| `MONGODB_EXTRA_PASSWORDS`               | Comma or semicolon separated list of passwords for the users specified in MONGODB_EXTRA_USERNAMES.                                             | `nil`                               |
| `MONGODB_EXTRA_DATABASES`               | Comma or semicolon separated list of databases to create at initialization time for the users specified in MONGODB_EXTRA_USERNAMES.            | `nil`                               |
| `ALLOW_EMPTY_PASSWORD`                  | Permit accessing MongoDB without setting any password                                                                                          | `no`                                |
| `MONGODB_REPLICA_SET_MODE`              | MongoDB replica set mode. Can be one of primary, secondary or arbiter                                                                          | `nil`                               |
| `MONGODB_REPLICA_SET_NAME`              | Name of the MongoDB replica set                                                                                                                | `$MONGODB_DEFAULT_REPLICA_SET_NAME` |
| `MONGODB_REPLICA_SET_KEY`               | MongoDB replica set key                                                                                                                        | `nil`                               |
| `MONGODB_INITIAL_PRIMARY_HOST`          | Hostname of the replica set primary node (necessary for arbiter and secondary nodes)                                                           | `nil`                               |
| `MONGODB_INITIAL_PRIMARY_PORT_NUMBER`   | Port of the replica set primary node (necessary for arbiter and secondary nodes)                                                               | `27017`                             |
| `MONGODB_INITIAL_PRIMARY_ROOT_PASSWORD` | Primary node root user password (necessary for arbiter and secondary nodes)                                                                    | `nil`                               |
| `MONGODB_INITIAL_PRIMARY_ROOT_USER`     | Primary node root username (necessary for arbiter and secondary nodes)                                                                         | `root`                              |
| `MONGODB_SET_SECONDARY_OK`              | Mark node as readable. Necessary for cases where the PVC is lost                                                                               | `no`                                |
| `MONGODB_DISABLE_ENFORCE_AUTH`          | By default, MongoDB authentication will be enforced. If set to true, MongoDB will not enforce authentication                                   | `false`                             |

#### Read-only environment variables

| `MONGODB_VOLUME_DIR`                      | Persistence base directory                                             | `$BITNAMI_VOLUME_DIR/mongodb`             |
| `MONGODB_BASE_DIR`                        | MongoDB installation directory                                         | `$BITNAMI_ROOT_DIR/mongodb`               |
| `MONGODB_CONF_DIR`                        | MongoDB configuration directory                                        | `$MONGODB_BASE_DIR/conf`                  |
| `MONGODB_DEFAULT_CONF_DIR`                | MongoDB default configuration directory                                | `$MONGODB_BASE_DIR/conf.default`          |
| `MONGODB_LOG_DIR`                         | MongoDB logs directory                                                 | `$MONGODB_BASE_DIR/logs`                  |
| `MONGODB_DATA_DIR`                        | MongoDB data directory                                                 | `${MONGODB_VOLUME_DIR}/data`              |
| `MONGODB_TMP_DIR`                         | MongoDB temporary directory                                            | `$MONGODB_BASE_DIR/tmp`                   |
| `MONGODB_BIN_DIR`                         | MongoDB executables directory                                          | `$MONGODB_BASE_DIR/bin`                   |
| `MONGODB_TEMPLATES_DIR`                   | Directory where the mongodb.conf template file is stored               | `$MONGODB_BASE_DIR/templates`             |
| `MONGODB_MONGOD_TEMPLATES_FILE`           | Path to the mongodb.conf template file                                 | `$MONGODB_TEMPLATES_DIR/mongodb.conf.tpl` |
| `MONGODB_CONF_FILE`                       | Path to MongoDB configuration file                                     | `$MONGODB_CONF_DIR/mongodb.conf`          |
| `MONGODB_KEY_FILE`                        | Path to the MongoDB replica set keyfile                                | `$MONGODB_CONF_DIR/keyfile`               |
| `MONGODB_DB_SHELL_FILE`                   | Path to MongoDB dbshell file                                           | `/.dbshell`                               |
| `MONGODB_RC_FILE`                         | Path to MongoDB rc file                                                | `/.mongorc.js`                            |
| `MONGOSH_DIR`                             | Path to mongosh directory                                              | `/.mongodb`                               |
| `MONGOSH_RC_FILE`                         | Path to mongosh rc file                                                | `/.mongoshrc.js`                          |
| `MONGODB_PID_FILE`                        | Path to the MongoDB PID file                                           | `$MONGODB_TMP_DIR/mongodb.pid`            |
| `MONGODB_LOG_FILE`                        | Path to the MongoDB log file                                           | `$MONGODB_LOG_DIR/mongodb.log`            |
| `MONGODB_INITSCRIPTS_DIR`                 | Path to the MongoDB container init scripts directory                   | `/docker-entrypoint-initdb.d`             |
| `MONGODB_DAEMON_USER`                     | MongoDB system user                                                    | `mongo`                                   |
| `MONGODB_DAEMON_GROUP`                    | MongoDB system group                                                   | `mongo`                                   |
| `MONGODB_DEFAULT_PORT_NUMBER`             | MongoDB port set at build time                                         | `27017`                                   |
| `MONGODB_DEFAULT_ENABLE_JOURNAL`          | Enable MongoDB journal at build time                                   | `true`                                    |
| `MONGODB_DEFAULT_DISABLE_SYSTEM_LOG`      | Disable MongoDB daemon system log set at build time                    | `false`                                   |
| `MONGODB_DEFAULT_ENABLE_DIRECTORY_PER_DB` | Use a separate folder for storing each database data set at build time | `false`                                   |
| `MONGODB_DEFAULT_ENABLE_IPV6`             | Use IPv6 for database connections set at build time                    | `false`                                   |
| `MONGODB_DEFAULT_SYSTEM_LOG_VERBOSITY`    | MongoDB daemon log level set at build time                             | `0`                                       |
| `MONGODB_DEFAULT_REPLICA_SET_NAME`        | Name of the MongoDB replica set at build time                          | `replicaset`                              |

- `MONGODB_EXTRA_FLAGS`: Flags to be appended to the `mongod` startup command. No defaults
- `MONGODB_CLIENT_EXTRA_FLAGS`: Flags to be appended to the `mongo` command which is used to connect to the (local or remote) `mongod` daemon. No defaults

- `MONGODB_DISABLE_SYSTEM_LOG`: Whether to enable/disable system log on MongoDB&reg;. Default: `false`. Possible values: `[true, false]`.
- `MONGODB_SYSTEM_LOG_VERBOSITY`: MongoDB&reg; system log verbosity level. Default: `0`. Possible values: `[0, 1, 2, 3, 4, 5]`. For more information about the verbosity levels please refer to the [MongoDB&reg; documentation](https://docs.mongodb.com/manual/reference/configuration-options/#systemLog.verbosity)

  In order to enable launching commands using numactl, set the `MONGODB_ENABLE_NUMACTL` variable to true. For more information on this, check the official [MongoDB documentation][(<https://docs.mongodb.com/manual/administration/production-notes/#configuring-numa-on-linux>)

- `MONGODB_ENABLE_IPV6`: Whether to enable/disable IPv6 on MongoDB&reg;. Default: `false`. Possible values: `[true, false]`

- `MONGODB_ENABLE_DIRECTORY_PER_DB`: Whether to enable/disable directoryPerDB on MongoDB&reg;. Default: `true`. Possible values: `[true, false]`

- `MONGODB_ENABLE_JOURNAL`: Whether to enable/disable journaling on MongoDB&reg;. Default: `true`. Possible values: `[true, false]`

Passing the `MONGODB_ROOT_PASSWORD` environment variable when running the image for the first time will set the password of `MONGODB_ROOT_USER` to the value of `MONGODB_ROOT_PASSWORD` and enable authentication on the MongoDB&reg; server. If unset, `MONGODB_ROOT_USER` defaults to `root`.

The `MONGODB_ROOT_USER` user is configured to have full administrative access to the MongoDB&reg; server. When `MONGODB_ROOT_PASSWORD` is not specified the server allows unauthenticated and unrestricted access.

You can create a user with restricted access to a database while starting the container for the first time. To do this, provide the `MONGODB_USERNAME`, `MONGODB_PASSWORD` and `MONGODB_DATABASE` environment variables.

- `MONGODB_REPLICA_SET_MODE`: The replication mode. Possible values `primary`/`secondary`/`arbiter`. No defaults.
- `MONGODB_REPLICA_SET_NAME`: MongoDB&reg; replica set name. Default: **replicaset**
- `MONGODB_PORT_NUMBER`: The port each MongoDB&reg; will use. Default: **27017**
- `MONGODB_INITIAL_PRIMARY_HOST`: MongoDB&reg; initial primary host, once the replicaset is created any node can be eventually promoted to be the primary. No defaults.
- `MONGODB_INITIAL_PRIMARY_PORT_NUMBER`: MongoDB&reg; initial primary node port, as seen by other nodes. Default: **27017**
- `MONGODB_ADVERTISED_HOSTNAME`: MongoDB&reg; advertised hostname. No defaults. It is recommended to pass this environment variable if you experience issues with ephemeral IPs. Setting this env var makes the nodes of the replica set to be configured with a hostname instead of the machine IP.
- `MONGODB_ADVERTISE_IP`: MongoDB&reg; advertised hostname is set to container ip. Default: **false**. Overrides `MONGODB_ADVERTISED_HOSTNAME`
- `MONGODB_ADVERTISED_PORT_NUMBER`: MongoDB&reg; advertised port number. No defaults. It is recommended to pass this environment variable if you have a proxy port forwarding requests to container.
- `MONGODB_REPLICA_SET_KEY`: MongoDB&reg; replica set key. Length should be greater than 5 characters and should not contain any special characters. Required for all nodes. No default.
- `MONGODB_ROOT_USER`: MongoDB&reg; root user name. Default: **root**.
- `MONGODB_ROOT_PASSWORD`: MongoDB&reg; root password. No defaults. Only for primary node.
- `MONGODB_INITIAL_PRIMARY_ROOT_PASSWORD`: MongoDB&reg; initial primary root password. No defaults. Only for secondaries and arbiter nodes.

