<!-- source: https://hub.docker.com/_/postgres fetched 2026-11-15, extract -->

```console
$ docker run --name some-postgres -e POSTGRES_PASSWORD=mysecretpassword -d postgres
```

```console
$ docker run -it --rm --network some-network postgres psql -h some-postgres -U postgres
psql (14.3)
Type "help" for help.

postgres=# SELECT 1;
 ?column? 
----------
        1
(1 row)
```

```yaml
# Use postgres/example user/password credentials

services:

  db:
    image: postgres
    restart: always
    # set shared memory limit when using docker compose
    shm_size: 128mb
    # or set shared memory limit when deploy via swarm stack
    #volumes:
    #  - type: tmpfs
    #    target: /dev/shm
    #    tmpfs:
    #      size: 134217728 # 128*2^20 bytes = 128Mb
    environment:
      POSTGRES_PASSWORD: example

  adminer:
    image: adminer
    restart: always
    ports:
      - 8080:8080
```

## Environment Variables

The PostgreSQL image uses several environment variables which are easy to miss. The only variable required is `POSTGRES_PASSWORD`, the rest are optional.

### `POSTGRES_PASSWORD`

This environment variable is required for you to use the PostgreSQL image. It must not be empty or undefined. This environment variable sets the superuser password for PostgreSQL. The default superuser is defined by the `POSTGRES_USER` environment variable.

### `POSTGRES_USER`

This optional environment variable is used in conjunction with `POSTGRES_PASSWORD` to set a user and its password. This variable will create the specified user with superuser power and a database with the same name. If it is not specified, then the default user of `postgres` will be used.

Be aware that if this parameter is specified, PostgreSQL will still show `The files belonging to this database system will be owned by user "postgres"` during initialization. This refers to the Linux system user (from `/etc/passwd` in the image) that the `postgres` daemon runs as, and as such is unrelated to the `POSTGRES_USER` option. See the section titled "Arbitrary `--user` Notes" for more details.

### `POSTGRES_DB`

This optional environment variable can be used to define a different name for the default database that is created when the image is first started. If it is not specified, then the value of `POSTGRES_USER` will be used.

### `POSTGRES_INITDB_ARGS`

This optional environment variable can be used to send arguments to `postgres initdb`. The value is a space separated string of arguments as `postgres initdb` would expect them. This is useful for adding functionality like data page checksums: `-e POSTGRES_INITDB_ARGS="--data-checksums"`.

### `POSTGRES_INITDB_WALDIR`

This optional environment variable can be used to define another location for the Postgres transaction log. By default the transaction log is stored in a subdirectory of the main Postgres data folder (`PGDATA`). Sometimes it can be desireable to store the transaction log in a different directory which may be backed by storage with different performance or reliability characteristics.

**Note:** on PostgreSQL 9.x, this variable is `POSTGRES_INITDB_XLOGDIR` (reflecting [the changed name of the `--xlogdir` flag to `--waldir` in PostgreSQL 10+](https://wiki.postgresql.org/wiki/New_in_postgres_10#Renaming_of_.22xlog.22_to_.22wal.22_Globally_.28and_location.2Flsn.29)).

### `POSTGRES_HOST_AUTH_METHOD`

This optional variable can be used to control the `auth-method` for `host` connections for `all` databases, `all` users, and `all` addresses. If unspecified then [`scram-sha-256` password authentication](https://www.postgresql.org/docs/14/auth-password.html) is used (in 14+; `md5` in older releases). On an uninitialized database, this will populate `pg_hba.conf` via this approximate line:

**Note 1:** It is not recommended to use `trust` since it allows anyone to connect without a password, even if one is set (like via `POSTGRES_PASSWORD`). For more information see the PostgreSQL documentation on [*Trust Authentication*](https://www.postgresql.org/docs/14/auth-trust.html).

**Note 2:** If you set `POSTGRES_HOST_AUTH_METHOD` to `trust`, then `POSTGRES_PASSWORD` is not required.

**Note 3:** If you set this to an alternative value (such as `scram-sha-256`), you might need additional `POSTGRES_INITDB_ARGS` for the database to initialize correctly (such as `POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256`).

### `PGDATA`

> **Important Change:** [the `PGDATA` environment variable of the image was changed to be version specific in PostgreSQL 18 and above](https://github.com/docker-library/postgres/pull/1259). For 18 it is `/var/lib/postgresql/18/docker`. Later versions will replace `18` with their respective major version (e.g., `/var/lib/postgresql/19/docker` for PostgreSQL `19.x`). The defined `VOLUME` was changed in 18 and above to `/var/lib/postgresql`. Mounts and volumes should be targeted at the updated location. This will allow users upgrading between PostgreSQL major releases to use the faster `--link` when running `pg_upgrade` and mounting `/var/lib/postgresql`.

Users who wish to opt-in to this change on older releases can do so by setting `PGDATA` explicitly (`--env PGDATA=/var/lib/postgresql/17/docker --volume some-postgres:/var/lib/postgresql`). To migrate pre-existing data, adjust the volume's folder structure appropriately first (moving all database files into a `PG_MAJOR/docker` subdirectory).

This (`PGDATA`) is an environment variable that is not Docker specific. Because the variable is used by the `postgres` server binary (see the [PostgreSQL docs](https://www.postgresql.org/docs/14/app-postgres.html#id-1.9.5.14.7)), the entrypoint script takes it into account.

```console
$ docker run --name some-postgres -e POSTGRES_PASSWORD_FILE=/run/secrets/postgres-passwd -d postgres
```

Currently, this is only supported for `POSTGRES_INITDB_ARGS`, `POSTGRES_PASSWORD`, `POSTGRES_USER`, and `POSTGRES_DB`.

These initialization files will be executed in sorted name order as defined by the current locale, which defaults to `en_US.utf8`. Any `*.sql` files will be executed by `POSTGRES_USER`, which defaults to the `postgres` superuser. It is recommended that any `psql` commands that are run inside of a `*.sh` script be executed as `POSTGRES_USER` by using the `--username "$POSTGRES_USER"` flag. This user will be able to connect without a password due to the presence of `trust` authentication for Unix socket connections made inside the container.

	```console
	$ # get the default config
	$ docker run -i --rm postgres cat /usr/share/postgresql/postgresql.conf.sample > my-postgres.conf

	$ # customize the config

	$ # run postgres with custom config
	$ docker run -d --name some-postgres -v "$PWD/my-postgres.conf":/etc/postgresql/postgresql.conf -e POSTGRES_PASSWORD=mysecretpassword postgres -c 'config_file=/etc/postgresql/postgresql.conf'
	```

	```console
	$ docker run -d --name some-postgres -e POSTGRES_PASSWORD=mysecretpassword postgres -c shared_buffers=256MB -c max_connections=200
	```

You can set locales in the Alpine-based images with `POSTGRES_INITDB_ARGS` to set a different locale. The following example will set the default locale for a newly initialized database to `de_DE.utf8`:

```console
$ docker run -d -e LANG=de_DE.utf8 -e POSTGRES_INITDB_ARGS="--locale-provider=icu --icu-locale=de-DE" -e POSTGRES_PASSWORD=mysecretpassword postgres:15-alpine 
```

The main caveat to note is that `postgres` doesn't care what UID it runs as (as long as the owner of `PGDATA` matches), but `initdb` *does* care (and needs the user to exist in `/etc/passwd`):

```console
$ docker run -it --rm --user www-data -e POSTGRES_PASSWORD=mysecretpassword postgres
The files belonging to this database system will be owned by user "www-data".
...

$ docker run -it --rm --user 1000:1000 -e POSTGRES_PASSWORD=mysecretpassword postgres
initdb: could not look up effective user ID 1000: user does not exist
```

