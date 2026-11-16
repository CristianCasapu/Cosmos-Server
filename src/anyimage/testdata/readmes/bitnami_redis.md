<!-- source: https://hub.docker.com/r/bitnami/redis fetched 2026-11-15, extract -->

```console
docker run --name redis REGISTRY_NAME/bitnami/redis:latest
```

> Note: You need to substitute the `REGISTRY_NAME` placeholder with a reference to your container registry.

Redis(R) provides a different range of [persistence options](https://redis.io/topics/persistence). This contanier uses *AOF persistence by default* but it is easy to overwrite that configuration by setting the following command when running the container  `/opt/bitnami/scripts/redis/run.sh --appendonly no`. Alternatively, you may use the `REDIS_AOF_ENABLED` env variable as explained in [Disabling AOF persistence](https://github.com/bitnami/containers/blob/main/bitnami/redis#disabling-aof-persistence).

| Name                             | Description                                      | Default Value                              |
|----------------------------------|--------------------------------------------------|--------------------------------------------|
| `REDIS_DATA_DIR`                 | Redis data directory                             | `${REDIS_VOLUME_DIR}/data`                 |
| `REDIS_OVERRIDES_FILE`           | Redis config overrides file                      | `${REDIS_MOUNTED_CONF_DIR}/overrides.conf` |
| `REDIS_DISABLE_COMMANDS`         | Commands to disable in Redis                     | `nil`                                      |
| `REDIS_DATABASE`                 | Default Redis database                           | `redis`                                    |
| `REDIS_AOF_ENABLED`              | Enable AOF                                       | `yes`                                      |
| `REDIS_RDB_POLICY`               | Enable RDB policy persistence                    | `nil`                                      |
| `REDIS_RDB_POLICY_DISABLED`      | Allows to enable RDB policy persistence          | `no`                                       |
| `REDIS_MASTER_HOST`              | Redis master host (used by slaves)               | `nil`                                      |
| `REDIS_MASTER_PORT_NUMBER`       | Redis master host port (used by slaves)          | `6379`                                     |
| `REDIS_PORT_NUMBER`              | Redis port number                                | `$REDIS_DEFAULT_PORT_NUMBER`               |
| `REDIS_ALLOW_REMOTE_CONNECTIONS` | Allow remote connection to the service           | `yes`                                      |
| `REDIS_REPLICATION_MODE`         | Redis replication mode (values: master, slave)   | `nil`                                      |
| `REDIS_REPLICA_IP`               | The replication announce ip                      | `nil`                                      |
| `REDIS_REPLICA_PORT`             | The replication announce port                    | `nil`                                      |
| `REDIS_EXTRA_FLAGS`              | Additional flags pass to 'redis-server' commands | `nil`                                      |
| `ALLOW_EMPTY_PASSWORD`           | Allow password-less access                       | `no`                                       |
| `REDIS_PASSWORD`                 | Password for Redis                               | `nil`                                      |
| `REDIS_MASTER_PASSWORD`          | Redis master node password                       | `nil`                                      |
| `REDIS_ACLFILE`                  | Redis ACL file                                   | `nil`                                      |
| `REDIS_IO_THREADS_DO_READS`      | Enable multithreading when reading socket        | `nil`                                      |
| `REDIS_IO_THREADS`               | Number of threads                                | `nil`                                      |
| `REDIS_TLS_ENABLED`              | Enable TLS                                       | `no`                                       |
| `REDIS_TLS_PORT_NUMBER`          | Redis TLS port (requires REDIS_ENABLE_TLS=yes)   | `6379`                                     |
| `REDIS_TLS_CERT_FILE`            | Redis TLS certificate file                       | `nil`                                      |
| `REDIS_TLS_CA_DIR`               | Directory containing TLS CA certificates         | `nil`                                      |
| `REDIS_TLS_KEY_FILE`             | Redis TLS key file                               | `nil`                                      |
| `REDIS_TLS_KEY_FILE_PASS`        | Redis TLS key file passphrase                    | `nil`                                      |
| `REDIS_TLS_CA_FILE`              | Redis TLS CA file                                | `nil`                                      |
| `REDIS_TLS_DH_PARAMS_FILE`       | Redis TLS DH parameter file                      | `nil`                                      |
| `REDIS_TLS_AUTH_CLIENTS`         | Enable Redis TLS client authentication           | `yes`                                      |
| `REDIS_SENTINEL_MASTER_NAME`     | Redis Sentinel master name                       | `nil`                                      |
| `REDIS_SENTINEL_HOST`            | Redis Sentinel host                              | `nil`                                      |
| `REDIS_SENTINEL_PORT_NUMBER`     | Redis Sentinel host port (used by slaves)        | `26379`                                    |
| `REDIS_SENTINEL_PASSWORD`        | Redis Sentinel password                          | `nil`                                      |

#### Read-only environment variables

| `REDIS_VOLUME_DIR`          | Persistence base directory            | `/bitnami/redis`                |
| `REDIS_BASE_DIR`            | Redis installation directory          | `${BITNAMI_ROOT_DIR}/redis`     |
| `REDIS_CONF_DIR`            | Redis configuration directory         | `${REDIS_BASE_DIR}/etc`         |
| `REDIS_DEFAULT_CONF_DIR`    | Redis default configuration directory | `${REDIS_BASE_DIR}/etc.default` |
| `REDIS_MOUNTED_CONF_DIR`    | Redis mounted configuration directory | `${REDIS_BASE_DIR}/mounted-etc` |
| `REDIS_CONF_FILE`           | Redis configuration file              | `${REDIS_CONF_DIR}/redis.conf`  |
| `REDIS_LOG_DIR`             | Redis logs directory                  | `${REDIS_BASE_DIR}/logs`        |
| `REDIS_LOG_FILE`            | Redis log file                        | `${REDIS_LOG_DIR}/redis.log`    |
| `REDIS_TMP_DIR`             | Redis temporary directory             | `${REDIS_BASE_DIR}/tmp`         |
| `REDIS_PID_FILE`            | Redis PID file                        | `${REDIS_TMP_DIR}/redis.pid`    |
| `REDIS_BIN_DIR`             | Redis executables directory           | `${REDIS_BASE_DIR}/bin`         |
| `REDIS_DAEMON_USER`         | Redis system user                     | `redis`                         |
| `REDIS_DAEMON_GROUP`        | Redis system group                    | `redis`                         |
| `REDIS_DEFAULT_PORT_NUMBER` | Redis port number (Build time)        | `6379`                          |

- `REDIS_DISABLE_COMMANDS`: Comma-separated list of Redis(R) commands to disable. Defaults to empty.

```console
docker run --name redis -e ALLOW_EMPTY_PASSWORD=yes REGISTRY_NAME/bitnami/redis:latest /opt/bitnami/scripts/redis/run.sh --maxmemory 100mb
```

Passing the `REDIS_PASSWORD` environment variable when running the image for the first time will set the Redis(R) server password to the value of `REDIS_PASSWORD` (or the content of the file specified in `REDIS_PASSWORD_FILE`).

**NOTE**: The at sign (`@`) is not supported for `REDIS_PASSWORD`.

**Warning** The Redis(R) database is always configured with remote access enabled. It's suggested that the `REDIS_PASSWORD` env variable is always specified to set a password. In case you want to access the database without a password set the environment variable `ALLOW_EMPTY_PASSWORD=yes`. **This is recommended only for development**.

By default the Redis(R) image expects all the available passwords to be set. In order to allow empty passwords, it is necessary to set the `ALLOW_EMPTY_PASSWORD=yes` env variable. This env variable is only recommended for testing or development purposes. We strongly recommend specifying the `REDIS_PASSWORD` for any other scenario.

Redis 6.0 features a [new multi-threading model](https://segmentfault.com/a/1190000040376111/en). You can set both `io-threads` and `io-threads-do-reads` though the env vars `REDIS_IO_THREADS` and `REDIS_IO_THREADS_DO_READS`

Redis(R) offers different [options](https://redis.io/topics/persistence) when it comes to persistence. By default, this image is set up to use the AOF (Append Only File) approach. Should you need to change this behaviour, setting the `REDIS_AOF_ENABLED=no` env variable will disable this feature.

Redis(R) offers [ACL](https://redis.io/topics/acl) since 6.0 which allows certain connections to be limited in terms of the commands that can be executed and the keys that can be accessed. We strongly recommend enabling ACL in production by specifying the `REDIS_ACLFILE`.

```console
docker run -name redis -e REDIS_ACLFILE=/opt/bitnami/redis/mounted-etc/users.acl -v /path/to/users.acl:/opt/bitnami/redis/mounted-etc/users.acl REGISTRY_NAME/bitnami/redis:latest
```

- `REDIS_REPLICATION_MODE`: The replication mode. Possible values `master`/`slave`. No defaults.
- `REDIS_REPLICA_IP`: The replication announce ip. Defaults to `$(get_machine_ip)` which return the ip of the container.
- `REDIS_REPLICA_PORT`: The replication announce port. Defaults to `REDIS_MASTER_PORT_NUMBER`.
- `REDIS_MASTER_HOST`: Hostname/IP of replication master (replica node parameter). No defaults.
- `REDIS_MASTER_PORT_NUMBER`: Server port of the replication master (replica node parameter). Defaults to `6379`.
- `REDIS_MASTER_PASSWORD`: Password to authenticate with the master (replica node parameter). No defaults. As an alternative, you can mount a file with the password and set the `REDIS_MASTER_PASSWORD_FILE` variable.

- `REDIS_TLS_ENABLED`: Whether to enable TLS for traffic or not. Defaults to `no`.
- `REDIS_TLS_PORT_NUMBER`: Port used for TLS secure traffic. Defaults to `6379`.
- `REDIS_TLS_CERT_FILE`: File containing the certificate file for the TLS traffic. No defaults.
- `REDIS_TLS_KEY_FILE`: File containing the key for certificate. No defaults.
- `REDIS_TLS_CA_FILE`: File containing the CA of the certificate (takes precedence over `REDIS_TLS_CA_DIR`). No defaults.
- `REDIS_TLS_CA_DIR`: Directory containing the CA certificates. No defaults.
- `REDIS_TLS_DH_PARAMS_FILE`: File containing DH params (in order to support DH based ciphers). No defaults.
- `REDIS_TLS_AUTH_CLIENTS`: Whether to require clients to authenticate or not. Defaults to `yes`.

When enabling TLS, conventional standard traffic is disabled by default. However this new feature is not mutually exclusive, which means it is possible to listen to both TLS and non-TLS connection simultaneously. To enable non-TLS traffic, set `REDIS_TLS_PORT_NUMBER` to another port different than `0`.

```console
docker run --name redis \
    -e ALLOW_EMPTY_PASSWORD=yes \
    -v /path/to/your_redis.conf:/opt/bitnami/redis/mounted-etc/redis.conf \
    -v /path/to/redis-data-persistence:/bitnami/redis/data \
    REGISTRY_NAME/bitnami/redis:latest
```

```console
docker run --name redis \
    -e ALLOW_EMPTY_PASSWORD=yes \
    -v /path/to/overrides.conf:/opt/bitnami/redis/mounted-etc/overrides.conf \
    REGISTRY_NAME/bitnami/redis:latest
```

When the value of `REDIS_RDB_POLICY_DISABLED` is `no` (default value) the Redis(R) default persistence strategy will be used. If you want to modify the default strategy, you can configure it through the `REDIS_RDB_POLICY` parameter.

- `OPENSSL_FIPS`: whether OpenSSL runs in FIPS mode or not. `yes` (default), `no`.

```bash
docker-compose down
## <a id="locate-your-volume-and-modify"></a> Locate your volume and modify the file tree
VOLUME=$(docker volume ls | grep "redis_data" | awk '{print $2}')
docker run --rm -i -v=${VOLUME}:/tmp/redis busybox find /tmp/redis/data -maxdepth 1 -exec mv {} /tmp/redis \;
docker run --rm -i -v=${VOLUME}:/tmp/redis busybox rm -rf /tmp/redis/{data,conf,.initialized}
## <a id="change-the-mount-point"></a> Change the mount point
sed -i -e 's#redis_data:/bitnami/redis#redis_data:/bitnami/redis/data#g' docker-compose.yml
## <a id="pull-the-latest-bitnami-redis"></a> Pull the latest bitnami/redis image
docker pull REGISTRY_NAME/bitnami/redis:latest
docker-compose up -d
```

