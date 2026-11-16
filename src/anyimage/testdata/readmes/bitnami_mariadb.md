<!-- source: https://hub.docker.com/r/bitnami/mariadb fetched 2026-11-15, extract -->

```console
docker run --name mariadb REGISTRY_NAME/bitnami/mariadb:latest
```

> Note: You need to substitute the `REGISTRY_NAME` placeholder with a reference to your container registry.

The following section describes the supported environment variables

| Name                              | Description                                                                                                               | Default Value           |
|-----------------------------------|---------------------------------------------------------------------------------------------------------------------------|-------------------------|
| `ALLOW_EMPTY_PASSWORD`            | Allow MariaDB access without any password.                                                                                | `no`                    |
| `MARIADB_AUTHENTICATION_PLUGIN`   | MariaDB authentication plugin to configure during the first initialization.                                               | `nil`                   |
| `MARIADB_ROOT_USER`               | MariaDB database root user.                                                                                               | `root`                  |
| `MARIADB_ROOT_PASSWORD`           | MariaDB database root user password.                                                                                      | `nil`                   |
| `MARIADB_USER`                    | MariaDB database user to create during the first initialization.                                                          | `nil`                   |
| `MARIADB_PASSWORD`                | Password for the MariaDB database user to create during the first initialization.                                         | `nil`                   |
| `MARIADB_DATABASE`                | MariaDB database to create during the first initialization.                                                               | `nil`                   |
| `MARIADB_MASTER_HOST`             | Address for the MariaDB master node.                                                                                      | `nil`                   |
| `MARIADB_MASTER_PORT_NUMBER`      | Port number for the MariaDB master node.                                                                                  | `3306`                  |
| `MARIADB_MASTER_ROOT_USER`        | MariaDB database root user of the master host.                                                                            | `root`                  |
| `MARIADB_MASTER_ROOT_PASSWORD`    | Password for the MariaDB database root user of the the master host.                                                       | `nil`                   |
| `MARIADB_MASTER_DELAY`            | MariaDB database replication delay.                                                                                       | `0`                     |
| `MARIADB_REPLICATION_USER`        | MariaDB replication database user.                                                                                        | `nil`                   |
| `MARIADB_REPLICATION_PASSWORD`    | Password for the MariaDB replication database user.                                                                       | `nil`                   |
| `MARIADB_PORT_NUMBER`             | Port number to use for the MariaDB Server service.                                                                        | `nil`                   |
| `MARIADB_REPLICATION_MODE`        | MariaDB replication mode.                                                                                                 | `nil`                   |
| `MARIADB_REPLICATION_SLAVE_DUMP`  | Make a dump on master and update slave MariaDB database                                                                   | `false`                 |
| `MARIADB_EXTRA_FLAGS`             | Extra flags to be passed to start the MariaDB Server.                                                                     | `nil`                   |
| `MARIADB_INIT_SLEEP_TIME`         | Sleep time when waiting for MariaDB init configuration operations to finish.                                              | `nil`                   |
| `MARIADB_CHARACTER_SET`           | MariaDB collation to use.                                                                                                 | `nil`                   |
| `MARIADB_COLLATE`                 | MariaDB collation to use.                                                                                                 | `nil`                   |
| `MARIADB_BIND_ADDRESS`            | MariaDB bind address.                                                                                                     | `nil`                   |
| `MARIADB_SQL_MODE`                | MariaDB Server SQL modes to enable.                                                                                       | `nil`                   |
| `MARIADB_UPGRADE`                 | MariaDB upgrade option.                                                                                                   | `AUTO`                  |
| `MARIADB_SKIP_TEST_DB`            | Whether to skip creating the test database.                                                                               | `no`                    |
| `MARIADB_CLIENT_ENABLE_SSL`       | Whether to force SSL for connections to the MariaDB database.                                                             | `no`                    |
| `MARIADB_REPLICATION_USE_SSL`     | Whether to force SSL for MariaDB replication.                                                                             | `$DB_CLIENT_ENABLE_SSL` |
| `MARIADB_CLIENT_SSL_CA_FILE`      | Path to CA certificate to use for SSL connections to the MariaDB database server.                                         | `nil`                   |
| `MARIADB_CLIENT_SSL_CERT_FILE`    | Path to client public key certificate to use for SSL connections to the MariaDB database server.                          | `nil`                   |
| `MARIADB_CLIENT_SSL_KEY_FILE`     | Path to client private key to use for SSL connections to the MariaDB database server.                                     | `nil`                   |
| `MARIADB_CLIENT_EXTRA_FLAGS`      | Whether to force SSL connections with the "mysql" CLI tool. Useful for applications that rely on the CLI instead of APIs. | `no`                    |
| `MARIADB_STARTUP_WAIT_RETRIES`    | Number of retries waiting for the database to be running.                                                                 | `300`                   |
| `MARIADB_STARTUP_WAIT_SLEEP_TIME` | Sleep time between retries waiting for the database to be running.                                                        | `2`                     |
| `MARIADB_ENABLE_SLOW_QUERY`       | Whether to enable slow query logs.                                                                                        | `0`                     |
| `MARIADB_LONG_QUERY_TIME`         | How much time, in seconds, defines a slow query.                                                                          | `10.0`                  |

#### Read-only environment variables

| `DB_FLAVOR`                     | SQL database flavor. Valid values: `mariadb` or `mysql`.     | `mariadb`                       |
| `DB_BASE_DIR`                   | Base path for MariaDB files.                                 | `${BITNAMI_ROOT_DIR}/mariadb`   |
| `DB_VOLUME_DIR`                 | MariaDB directory for persisted files.                       | `${BITNAMI_VOLUME_DIR}/mariadb` |
| `DB_DATA_DIR`                   | MariaDB directory for data files.                            | `${DB_VOLUME_DIR}/data`         |
| `DB_BIN_DIR`                    | MariaDB directory where executable binary files are located. | `${DB_BASE_DIR}/bin`            |
| `DB_SBIN_DIR`                   | MariaDB directory where service binary files are located.    | `${DB_BASE_DIR}/sbin`           |
| `DB_CONF_DIR`                   | MariaDB configuration directory.                             | `${DB_BASE_DIR}/conf`           |
| `DB_DEFAULT_CONF_DIR`           | MariaDB default configuration directory.                     | `${DB_BASE_DIR}/conf.default`   |
| `DB_LOGS_DIR`                   | MariaDB logs directory.                                      | `${DB_BASE_DIR}/logs`           |
| `DB_TMP_DIR`                    | MariaDB directory for temporary files.                       | `${DB_BASE_DIR}/tmp`            |
| `DB_CONF_FILE`                  | Main MariaDB configuration file.                             | `${DB_CONF_DIR}/my.cnf`         |
| `DB_PID_FILE`                   | MariaDB PID file.                                            | `${DB_TMP_DIR}/mysqld.pid`      |
| `DB_SOCKET_FILE`                | MariaDB Server socket file.                                  | `${DB_TMP_DIR}/mysql.sock`      |
| `DB_DAEMON_USER`                | Users that will execute the MariaDB Server process.          | `mysql`                         |
| `DB_DAEMON_GROUP`               | Group that will execute the MariaDB Server process.          | `mysql`                         |
| `MARIADB_DEFAULT_PORT_NUMBER`   | Default port number to use for the MariaDB Server service.   | `3306`                          |
| `MARIADB_DEFAULT_CHARACTER_SET` | Default MariaDB character set.                               | `utf8mb4`                       |
| `MARIADB_DEFAULT_BIND_ADDRESS`  | Default MariaDB bind address.                                | `0.0.0.0`                       |

- `MARIADB_EXTRA_FLAGS`: Flags to be appended to the startup command. No defaults

- `MARIADB_CHARACTER_SET`: The default character set to use. Default: `utf8`
- `MARIADB_COLLATE`: The default collation to use. Default: `utf8_general_ci`

- `MARIADB_ROOT_USER`: The database admin user. Defaults to `root`.
- `MARIADB_ROOT_PASSWORD`: The database admin user password. No defaults.
- `MARIADB_ROOT_PASSWORD_FILE`: Path to a file that contains the admin user password. This will override the value specified in `MARIADB_ROOT_PASSWORD`. No defaults.

Passing the `MARIADB_ROOT_PASSWORD` environment variable when running the image for the first time will set the password of the `MARIADB_ROOT_USER` user to the value of `MARIADB_ROOT_PASSWORD`.

**Warning** The `MARIADB_ROOT_USER` user is always created with remote access. It's suggested that the `MARIADB_ROOT_PASSWORD` env variable is always specified to set a password for the `MARIADB_ROOT_USER` user. In case you want to allow the `MARIADB_ROOT_USER` user to access the database without a password set the environment variable `ALLOW_EMPTY_PASSWORD=yes`. **This is recommended only for development**.

By default the MariaDB image expects all the available passwords to be set. In order to allow empty passwords, it is necessary to set the `ALLOW_EMPTY_PASSWORD=yes` env variable. This env variable is only recommended for testing or development purposes. We strongly recommend specifying the `MARIADB_ROOT_PASSWORD` for any other scenario.

By passing the `MARIADB_DATABASE` environment variable when running the image for the first time, a database will be created. This is useful if your application requires that a database already exists, saving you from having to manually create the database using the MySQL client.

You can create a restricted database user that only has permissions for the database created with the [`MARIADB_DATABASE`](#creating-a-database-on-first) environment variable. To do this, provide the `MARIADB_USER` environment variable and to set a password for the database user provide the `MARIADB_PASSWORD` variable (alternatively, you can set the `MARIADB_PASSWORD_FILE` with the path to a file that contains the user password). MariaDB supports different authentication mechanisms, such as `pam` or `mysql_native_password`. To set it, use the `MARIADB_AUTHENTICATION_PLUGIN` variable.

**Note!** The `root` user will be created with remote access and without a password if `ALLOW_EMPTY_PASSWORD` is enabled. Please provide the `MARIADB_ROOT_PASSWORD` env variable instead if you want to set a password for the `root` user.

To disable the test database in the Bitnami MariaDB container, set the `MARIADB_SKIP_TEST_DB` environment variable to `yes` during the first boot of the container.

- `MARIADB_ENABLE_SLOW_QUERY`: Whether to enable slow query logs. Default: `0`
- `MARIADB_LONG_QUERY_TIME`: How much time, in seconds, defines a slow query. Default: `10.0`

```console
$ docker run -d -e MARIADB_ENABLE_SLOW_QUERY=1 -e ALLOW_EMPTY_PASSWORD=yes --name my-mariadb-container bitnami/mariadb
# wait a bit for the initialization process...
$ docker exec -it my-mariadb-container mysqldumpslow
Reading mysql slow query log from /bitnami/mariadb/data/<hostname>-slow.log
Count: 1  Time=0.01s (0s)  Lock=0.00s (0s)  Rows=0.0 (0), root[root]@localhost
  GRANT ALL PRIVILEGES ON *.* TO 'S'@'S' WITH GRANT OPTION

Count: 1  Time=0.01s (0s)  Lock=0.00s (0s)  Rows=0.0 (0), root[root]@localhost
  CREATE USER 'S'@'S'

Count: 1  Time=0.01s (0s)  Lock=0.00s (0s)  Rows=0.0 (0), root[root]@localhost
  DELETE FROM mysql.user WHERE user not in ('S','S')

Count: 1  Time=0.00s (0s)  Lock=0.00s (0s)  Rows=0.0 (0), root[root]@localhost
  flush privileges
(...)
```

In some platforms, the filesystem used for persistence could be slow. That could cause the database to take extra time to be ready. If that's the case, you can configure the `MARIADB_INIT_SLEEP_TIME` environment variable to make the initialization script to wait extra time (in seconds) before proceeding with the configuration operations.

- `MARIADB_REPLICATION_MODE`: The replication mode. Possible values `master`/`slave`. No defaults.
- `MARIADB_REPLICATION_USER`: The replication user created on the master on first run. No defaults.
- `MARIADB_REPLICATION_PASSWORD`: The replication users password. No defaults.
- `MARIADB_REPLICATION_PASSWORD_FILE`: Path to a file that contains the replication user password. This will override the value specified in `MARIADB_REPLICATION_PASSWORD`. No defaults.
- `MARIADB_MASTER_HOST`: Hostname/IP of replication master (slave parameter). No defaults.
- `MARIADB_MASTER_PORT_NUMBER`: Server port of the replication master (slave parameter). Defaults to `3306`.
- `MARIADB_MASTER_ROOT_USER`: User on replication master with access to `MARIADB_DATABASE` (slave parameter). Defaults to `root`
- `MARIADB_MASTER_ROOT_PASSWORD`: Password of user on replication master with access to `MARIADB_DATABASE` (slave parameter). No defaults.
- `MARIADB_MASTER_ROOT_PASSWORD_FILE`: Path to a file that contains the password of user on replication master with access to `MARIADB_DATABASE`. This will override the value specified in `MARIADB_MASTER_ROOT_PASSWORD`. No defaults.
- `MARIADB_MASTER_DELAY`: The database replication delay (slave parameter). Defaults to `0`.

```console
docker run --name mariadb  -e ALLOW_EMPTY_PASSWORD=yes -v /path/to/my.cnf:/opt/bitnami/mariadb/conf/my.cnf:ro REGISTRY_NAME/bitnami/mariadb:latest
```

