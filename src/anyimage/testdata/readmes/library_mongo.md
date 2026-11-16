<!-- source: https://hub.docker.com/_/mongo fetched 2026-11-15, extract -->

By default Mongo's configuration requires no authentication for access, even for the administrative user. It is highly recommended to set a root user name and password if you plan on exposing your Mongo instance to the internet. See the "`MONGO_INITDB_ROOT_USERNAME`, `MONGO_INITDB_ROOT_PASSWORD`" section below for instructions and the [MongoDB Security documentation](https://www.mongodb.com/docs/manual/security/) for a more complete treatment.

```console
$ docker run --name some-mongo -d mongo:tag
```

```console
$ docker run -it --network some-network --rm mongo mongosh --host some-mongo test
```

```yaml
services:
  mongo:
    image: mongo
    restart: always
    environment:
      MONGO_INITDB_ROOT_USERNAME: root
      MONGO_INITDB_ROOT_PASSWORD: example

  mongo-express:
    image: mongo-express
    restart: always
    ports:
      - 8081:8081
    environment:
      ME_CONFIG_MONGODB_URL: mongodb://root:example@mongo:27017/
      ME_CONFIG_BASICAUTH_ENABLED: true
      ME_CONFIG_BASICAUTH_USERNAME: mongoexpressuser
      ME_CONFIG_BASICAUTH_PASSWORD: mongoexpresspass
```

```console
$ docker run --name some-mongo -d mongo --profile 1
```

```yaml
services:
  mongo:
    image: mongo
    command: --profile 1
```

```console
$ docker run -it --rm mongo --help
```

```console
$ docker run --name some-mongo -v /my/custom:/etc/mongo -d mongo --config /etc/mongo/mongod.conf
```

## Environment Variables

### `MONGO_INITDB_ROOT_USERNAME`, `MONGO_INITDB_ROOT_PASSWORD`

These variables, used in conjunction, create a new user and set that user's password. This user is created in the `admin` [authentication database](https://www.mongodb.com/docs/manual/core/security-users/#authentication-database) and given [the role of `root`](https://www.mongodb.com/docs/manual/reference/built-in-roles/#mongodb-authrole-root), which is [a "superuser" role](https://www.mongodb.com/docs/manual/reference/built-in-roles/#superuser-roles).

```console
$ docker run -d --network some-network --name some-mongo \
	-e MONGO_INITDB_ROOT_USERNAME=mongoadmin \
	-e MONGO_INITDB_ROOT_PASSWORD=secret \
	mongo

$ docker run -it --rm --network some-network mongo \
	mongosh --host some-mongo \
		-u mongoadmin \
		-p secret \
		--authenticationDatabase admin \
		some-db
> db.getName();
some-db
```

### `MONGO_INITDB_DATABASE`

This variable allows you to specify the name of a database to be used for creation scripts in `/docker-entrypoint-initdb.d/*.js` (see *Initializing a fresh instance* below). MongoDB is fundamentally designed for "create on first use", so if you do not insert data with your JavaScript files, then no database is created.

```console
$ docker run --name some-mongo -e MONGO_INITDB_ROOT_PASSWORD_FILE=/run/secrets/mongo-root -d mongo
```

Currently, this is only supported for `MONGO_INITDB_ROOT_USERNAME` and `MONGO_INITDB_ROOT_PASSWORD`.

When a container is started for the first time it will execute files with extensions `.sh` and `.js` that are found in `/docker-entrypoint-initdb.d`. Files will be executed in alphabetical order. `.js` files will be executed by `mongosh` (`mongo` on versions below 6) using the database specified by the `MONGO_INITDB_DATABASE` variable, if it is present, or `test` otherwise. You may also switch databases within the `.js` script.

In addition to the `/docker-entrypoint-initdb.d` behavior documented above (which is a simple way to configure users for authentication for less complicated deployments), this image also supports `MONGO_INITDB_ROOT_USERNAME` and `MONGO_INITDB_ROOT_PASSWORD` for creating a simple user with [the role `root`](https://www.mongodb.com/docs/manual/reference/built-in-roles/#mongodb-authrole-root) in the `admin` [authentication database](https://www.mongodb.com/docs/manual/core/security-users/#authentication-database), as described in the *Environment Variables* section above.

	```console
	$ docker run --name some-mongo -v /my/own/datadir:/data/db -d mongo
	```

