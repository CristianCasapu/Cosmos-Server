# Bitnami Secure Image for MariaDB

> MariaDB is an open source, community-developed SQL database server that is widely in use around the world due to its enterprise features, flexibility, and collaboration with leading tech firms.

[Overview of MariaDB](https://mariadb.org/)
Trademarks: This software listing is packaged by Bitnami. The respective trademarks mentioned in the offering are owned by the respective companies, and use of them does not imply any affiliation or endorsement.

## <a id="tl-dr"></a> TL;DR

```console
docker run --name mariadb REGISTRY_NAME/bitnami/mariadb:latest
```

> Note: You need to substitute the `REGISTRY_NAME` placeholder with a reference to your container registry.

## <a id="using-docker-compose-yml"></a> Using `docker-compose.yml`

The docker-compose.yaml file of this container can be found in the [Bitnami Containers repository](https://github.com/bitnami/containers/).

[https://github.com/bitnami/containers/tree/main/bitnami/mariadb/docker-compose.yml](https://github.com/bitnami/containers/tree/main/bitnami/mariadb/docker-compose.yml)

Please be aware this file has not undergone internal testing. Consequently, we advise its use exclusively for development or testing purposes. For production-ready deployments, we highly recommend utilizing its associated [Bitnami Helm chart](https://github.com/bitnami/charts/tree/main/bitnami/mariadb).

## <a id="choosing-between-the-standard-and"></a> Choosing between the _Standard_ and _Minimal_ image

This asset is available in two flavors: _Standard_ and _Minimal_; designed to address different use cases and operational needs.

### <a id="standard-images"></a> Standard images

The standard images are full-featured, production-ready containers built on top of secure base operating systems. They include:

- The complete runtime and commonly used system tools.
- A familiar Linux environment (shell, package manager, debugging utilities).
- Full compatibility with most CI/CD pipelines and existing workloads.

Recommended for:

- Development and testing environments.
- Workloads requiring package installation or debugging tools.
- Applications that depend on system utilities or shared libraries.

### <a id="minimal-images"></a> Minimal images

The minimal images are optimized, distroless-style containers derived from a stripped-down base. They only ship what’s strictly necessary to run the application; no shell, package manager, or extra libraries. They provide:

- Smaller size: Faster pull and startup times.
- Reduced attack surface: Fewer components and potential vulnerabilities.
- Simpler maintenance: Fewer dependencies to patch or update.

Recommended for:

- Production environments prioritizing performance and security.
- Regulated or security-sensitive workloads
- Containers built via multi-stage builds (e.g., Golang static binaries).

## <a id="how-to-deploy-mariadb-in"></a> How to deploy MariaDB in Kubernetes?

Deploying Bitnami applications as Helm Charts is the easiest way to get started with our applications on Kubernetes. Read more about the installation in the [Bitnami MariaDB Chart GitHub repository](https://github.com/bitnami/charts/tree/master/bitnami/mariadb).

## <a id="why-use-a-non-root"></a> Why use a non-root container?

Non-root container images add an extra layer of security and are generally recommended for production environments. However, because they run as a non-root user, privileged tasks are typically off-limits. Learn more about non-root containers [in our docs](https://techdocs.broadcom.com/us/en/vmware-tanzu/application-catalog/tanzu-application-catalog/services/tac-doc/apps-tutorials-work-with-non-root-containers-index.html).

## <a id="supported-tags-and-respective-dockerfile"></a> Supported tags and respective `Dockerfile` links

Learn more about the Bitnami tagging policy and the difference between rolling tags and immutable tags [in our documentation page](https://techdocs.broadcom.com/us/en/vmware-tanzu/application-catalog/tanzu-application-catalog/services/tac-doc/apps-tutorials-understand-rolling-tags-containers-index.html).

## <a id="get-this-image"></a> Get this image

The Bitnami MariaDB Docker image is only available to [Bitnami Secure Images](https://bitnami.com) customers.

## <a id="persisting-your-database"></a> Persisting your database

If you remove the container all your data will be lost, and the next time you run the image the database will be reinitialized. To avoid this loss of data, you should mount a volume that will persist even after the container is removed.

For persistence you should mount a directory at the `/bitnami/mariadb` path. If the mounted directory is empty, it will be initialized on the first run.

> **NOTE** As this is a non-root container, the mounted files and directories must have the proper permissions for the UID `1001`.

## <a id="connecting-to-other-containers"></a> Connecting to other containers

Using [Docker container networking](https://docs.docker.com/engine/userguide/networking/), a MariaDB server running inside a container can easily be accessed by your application containers.

Containers attached to the same network can communicate with each other using the container name as the hostname.

## <a id="configuration"></a> Configuration

The following section describes the supported environment variables

### <a id="environment-variables"></a> Environment variables

The following tables list the main variables you can set.

#### Customizable environment variables

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

| Name                            | Description                                                  | Value                           |
|---------------------------------|--------------------------------------------------------------|---------------------------------|
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

### <a id="initializing-a-new-instance"></a> Initializing a new instance

When the container is executed for the first time, it will execute the files with extensions `.sh`, `.sql` and `.sql.gz` located at `/docker-entrypoint-startdb.d`.

In order to have your custom files inside the docker image you can mount them as a volume.

Take into account those scripts are treated differently depending on the extension. While the `.sh` scripts are executed in all the nodes; the `.sql` and `.sql.gz` scripts are only executed in the master nodes. The reason behind this differentiation is that the `.sh` scripts allow adding conditions to determine what is the node running the script, while these conditions can't be set using `.sql` nor `sql.gz` files. This way it is possible to cover different use cases depending on their needs.

> **NOTE** If you are importing large databases, it is recommended to import them as `.sql` instead of `.sql.gz`, as the latter one needs to be decompressed on the fly and not allowing for additional optimizations to import large files.

### <a id="passing-extra-command-line-flags"></a> Passing extra command-line flags to mysqld startup

Passing extra command-line flags to the mysqld service command is possible through the following env var:

- `MARIADB_EXTRA_FLAGS`: Flags to be appended to the startup command. No defaults

### <a id="setting-character-set-and-collation"></a> Setting character set and collation

It is possible to configure the character set and collation used by default by the database with the following environment variables:

- `MARIADB_CHARACTER_SET`: The default character set to use. Default: `utf8`
- `MARIADB_COLLATE`: The default collation to use. Default: `utf8_general_ci`

### <a id="setting-the-root-password-on"></a> Setting the root password on first run

The root user and password can easily be setup with the Bitnami MariaDB Docker image using the following environment variables:

- `MARIADB_ROOT_USER`: The database admin user. Defaults to `root`.
- `MARIADB_ROOT_PASSWORD`: The database admin user password. No defaults.
- `MARIADB_ROOT_PASSWORD_FILE`: Path to a file that contains the admin user password. This will override the value specified in `MARIADB_ROOT_PASSWORD`. No defaults.

Passing the `MARIADB_ROOT_PASSWORD` environment variable when running the image for the first time will set the password of the `MARIADB_ROOT_USER` user to the value of `MARIADB_ROOT_PASSWORD`.

**Warning** The `MARIADB_ROOT_USER` user is always created with remote access. It's suggested that the `MARIADB_ROOT_PASSWORD` env variable is always specified to set a password for the `MARIADB_ROOT_USER` user. In case you want to allow the `MARIADB_ROOT_USER` user to access the database without a password set the environment variable `ALLOW_EMPTY_PASSWORD=yes`. **This is recommended only for development**.

### <a id="allowing-empty-passwords"></a> Allowing empty passwords

By default the MariaDB image expects all the available passwords to be set. In order to allow empty passwords, it is necessary to set the `ALLOW_EMPTY_PASSWORD=yes` env variable. This env variable is only recommended for testing or development purposes. We strongly recommend specifying the `MARIADB_ROOT_PASSWORD` for any other scenario.

### <a id="creating-a-database-on-first"></a> Creating a database on first run

By passing the `MARIADB_DATABASE` environment variable when running the image for the first time, a database will be created. This is useful if your application requires that a database already exists, saving you from having to manually create the database using the MySQL client.

### <a id="creating-a-database-user-on"></a> Creating a database user on first run

You can create a restricted database user that only has permissions for the database created with the [`MARIADB_DATABASE`](#creating-a-database-on-first) environment variable. To do this, provide the `MARIADB_USER` environment variable and to set a password for the database user provide the `MARIADB_PASSWORD` variable (alternatively, you can set the `MARIADB_PASSWORD_FILE` with the path to a file that contains the user password). MariaDB supports different authentication mechanisms, such as `pam` or `mysql_native_password`. To set it, use the `MARIADB_AUTHENTICATION_PLUGIN` variable.

**Note!** The `root` user will be created with remote access and without a password if `ALLOW_EMPTY_PASSWORD` is enabled. Please provide the `MARIADB_ROOT_PASSWORD` env variable instead if you want to set a password for the `root` user.

### <a id="disable-creation-of-test-database"></a> Disable creation of test database

By default MariaDB creates a test database. In order to disable the creation of this test database, the flag `--skip-test-db` can be passed to `mysql_install_db`. This function is only on MariaDB >= `10.5`.

To disable the test database in the Bitnami MariaDB container, set the `MARIADB_SKIP_TEST_DB` environment variable to `yes` during the first boot of the container.

### <a id="slow-query-logs"></a> Slow query logs

By default MariaDB doesn't enable [slow query log](https://mariadb.com/kb/en/slow-query-log-overview/) to record the SQL queries that take a long time to perform. You can modify these settings using the following environment variables:

- `MARIADB_ENABLE_SLOW_QUERY`: Whether to enable slow query logs. Default: `0`
- `MARIADB_LONG_QUERY_TIME`: How much time, in seconds, defines a slow query. Default: `10.0`

Slow queries information is logged to the `<data-dir>/<hostname>-slow.log` file by default, and you can easily check it with the `mysqldumpslow` tool ([link to docs](https://mariadb.com/kb/en/mysqldumpslow/)):

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

### <a id="slow-filesystems"></a> Slow filesystems

In some platforms, the filesystem used for persistence could be slow. That could cause the database to take extra time to be ready. If that's the case, you can configure the `MARIADB_INIT_SLEEP_TIME` environment variable to make the initialization script to wait extra time (in seconds) before proceeding with the configuration operations.

### <a id="setting-up-a-replication-cluster"></a> Setting up a replication cluster

A **zero downtime** MariaDB master-slave [replication](https://dev.mysql.com/doc/refman/5.7/en/server-option-variable-reference.html) cluster can easily be setup with the Bitnami MariaDB Docker image using the following environment variables:

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

In a replication cluster you can have one master and zero or more slaves. When replication is enabled the master node is in read-write mode, while the slaves are in read-only mode. For best performance its advisable to limit the reads to the slaves.

> **NOTE**: you can use the [mariadb-galera image](https://github.com/bitnami/containers/blob/main/bitnami/mariadb-galera) to set up a master-master replication cluster

### <a id="configuration-file"></a> Configuration file

The image looks for user-defined configurations in `/opt/bitnami/mariadb/conf/my_custom.cnf`. Create a file named `my_custom.cnf` and mount it at `/opt/bitnami/mariadb/conf/my_custom.cnf`.

#### Overwrite the main Configuration file

It is also possible to use your custom `my.cnf` and overwrite the main configuration file.

```console
docker run --name mariadb  -e ALLOW_EMPTY_PASSWORD=yes -v /path/to/my.cnf:/opt/bitnami/mariadb/conf/my.cnf:ro REGISTRY_NAME/bitnami/mariadb:latest
```

## <a id="customize-this-image"></a> Customize this image

The Bitnami MariaDB Docker image is designed

_Note: the README for this container is longer than the DockerHub length limit of 25000, so it has been trimmed. The full README can be found at https://techdocs.broadcom.com/us/en/vmware-tanzu/bitnami-secure-images/bitnami-secure-images/services/bsi-app-doc/apps-containers-mariadb-index.html_