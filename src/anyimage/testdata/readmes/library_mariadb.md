<!-- source: https://hub.docker.com/_/mariadb fetched 2026-11-15, extract -->

```console
$ docker run --name some-mariadb -p 3306:3306 mariadb:latest
```

```console
$ docker run --detach --name some-mariadb --env MARIADB_ROOT_PASSWORD=my-secret-pw mariadb:latest
```

```console
$ docker run --detach --name some-mariadb --env MARIADB_ALLOW_EMPTY_ROOT_PASSWORD=1 mariadb:latest
```

```console
$ docker run --detach --name some-mariadb --env MARIADB_RANDOM_ROOT_PASSWORD=1 mariadb:latest
```

```yaml
# Use root/example as user/password credentials

services:

  db:
    image: mariadb
    restart: always
    environment:
      MARIADB_ROOT_PASSWORD: example

  adminer:
    image: adminer
    restart: always
    ports:
      - 8080:8080
```

```console
$ docker run --detach --name some-mariadb --env MARIADB_USER=example-user --env MARIADB_PASSWORD=my_cool_secret --env MARIADB_DATABASE=exmple-database --env MARIADB_ROOT_PASSWORD=my-secret-pw mariadb:latest
```

```console
$ docker network create some-network 
$ docker run --detach --network some-network --name some-mariadb --env MARIADB_USER=example-user --env MARIADB_PASSWORD=my_cool_secret --env MARIADB_ROOT_PASSWORD=my-secret-pw mariadb:latest
$ docker run --detach --network some-network --name some-application --env APP_DB_HOST=some-mariadb --env APP_DB_USER=example-user --env APP_DB_PASSWD=my_cool_secret some-application
```

... where `some-network` is a newly created network (other than `bridge` as the default network), `some-mariadb` is the name you want to assign to your container, `my-secret-pw` is the password to be set for the MariaDB root user. See the list above for relevant tags to match your needs and environment. `some-application` and then environment variable `APP_DB_HOST`, `APP_DB_USER` and `APP_DB_PASSWD` are the application's configuration for its database connection.

```console
$ docker run -it --network some-network --rm mariadb mariadb -h some-mariadb -u example-user
```

```console
$ docker run -it --rm mariadb mariadb --host <server container IP> --user example-user --password --database test
```

```console
$ docker run --volume /backup-volume:/backup --rm mariadb:10.6.15 mariadb-backup --help
```

```console
$ docker run --name some-mariadb -v /my/custom:/etc/mysql/conf.d --rm mariadb:latest my_print_defaults --mariadbd
```

```console
$ docker run --name some-mariadb -e MARIADB_ROOT_PASSWORD=my-secret-pw -d mariadb:latest --port 3808
```

```console
$ docker run --rm mariadb:latest --verbose --help
```

## Environment Variables

When you start the `mariadb` image, you can adjust the initialization of the MariaDB instance by passing one or more environment variables on the `docker run` command line. Do note that all of the variables, except `MARIADB_AUTO_UPGRADE`, will have no effect if you start the container with a data directory that already contains a database. I.e. any pre-existing database will always be left untouched on container startup.

One of `MARIADB_RANDOM_ROOT_PASSWORD`, `MARIADB_ROOT_PASSWORD_HASH`, `MARIADB_ROOT_PASSWORD` or `MARIADB_ALLOW_EMPTY_ROOT_PASSWORD` (or equivalents, including `*_FILE`), is required. The other environment variables are optional.

### `MARIADB_AUTO_UPGRADE`

When this environment variable is set, this will run the [mariadb-upgrade](https://mariadb.com/kb/en/mariadb-upgrade/), if needed, so any changes in the MariaDB system tables required to expose new features will be made. This may impeed some [downgrade options](https://mariadb.com/kb/en/downgrading-between-major-versions-of-mariadb/). Unless the environment variable `MARIADB_DISABLE_UPGRADE_BACKUP` is set, there will be a backup of the system tables created as `system_mysql_backup_*.sql.zst` in the top level of the data directory to assist in the downgrade if needed.

```console
$ docker run --name some-mysql -e MARIADB_ROOT_PASSWORD_FILE=/run/secrets/mariadb-root -d mariadb:latest
```

When a container is started for the first time, a new database with the specified name will be created and initialized with the provided configuration variables. Furthermore, it will execute files with extensions `.sh`, `.sql`, `.sql.gz`, `.sql.xz` and `.sql.zst` that are found in `/docker-entrypoint-initdb.d`. Files will be executed in alphabetical order. `.sh` files without file execute permission are sourced rather than executed. You can easily populate your `mariadb` services by [mounting a SQL dump into that directory](https://docs.docker.com/storage/bind-mounts/) and provide [custom images](https://docs.docker.com/reference/dockerfile/) with contributed data. SQL files will be imported by default to the database specified by the `MARIADB_DATABASE` variable.

	```console
	$ docker run --name some-mariadb -v /my/own/datadir:/var/lib/mysql:Z -e MARIADB_ROOT_PASSWORD=my-secret-pw -d mariadb:latest
	```

If you start your `mariadb` container instance with a data directory that already contains a database (specifically, a `mysql` subdirectory), no environment variables that control initialization will be needed or examined, and no pre-existing databases will be changed. The only exception is the non-default `MARIADB_AUTO_UPGRADE` environment variable, that might cause `mysql_upgrade`/`mariadb-upgrade` to run, which might change the system tables.

