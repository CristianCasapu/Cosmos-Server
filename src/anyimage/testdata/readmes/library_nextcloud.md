**Note:** the description for this image is longer than the Hub length limit of 25000, so has been trimmed.  The full description can be found at [https://github.com/docker-library/docs/tree/master/nextcloud/README.md](https://github.com/docker-library/docs/tree/master/nextcloud/README.md).  See also [docker/hub-feedback#238](https://github.com/docker/hub-feedback/issues/238) and [docker/roadmap#475](https://github.com/docker/roadmap/issues/475).

# IMPORTANT NOTE

⚠️⚠️⚠️ This image is maintained by community volunteers and designed for expert use. For quick and easy deployment that supports the full set of Nextcloud Hub features, use the [Nextcloud All-in-One docker container](https://github.com/nextcloud/all-in-one#nextcloud-all-in-one) maintained by Nextcloud GmbH.

# Quick reference

-	**Maintained by**:  
	[the Nextcloud community](https://github.com/nextcloud/docker)

-	**Where to get help**:  
	[the Docker Community Slack](https://dockr.ly/comm-slack), [Server Fault](https://serverfault.com/help/on-topic), [Unix & Linux](https://unix.stackexchange.com/help/on-topic), or [Stack Overflow](https://stackoverflow.com/help/on-topic)

# Supported tags and respective `Dockerfile` links

-	[`32.0.15-apache`, `32.0-apache`, `32-apache`, `32.0.15`, `32.0`, `32`](https://github.com/nextcloud/docker/blob/7e9e152f77372845195ed28dc65569df74f582ff/32/apache/Dockerfile)

-	[`32.0.15-fpm`, `32.0-fpm`, `32-fpm`](https://github.com/nextcloud/docker/blob/7e9e152f77372845195ed28dc65569df74f582ff/32/fpm/Dockerfile)

-	[`32.0.15-fpm-alpine`, `32.0-fpm-alpine`, `32-fpm-alpine`](https://github.com/nextcloud/docker/blob/7e9e152f77372845195ed28dc65569df74f582ff/32/fpm-alpine/Dockerfile)

-	[`33.0.11-apache`, `33.0-apache`, `33-apache`, `33.0.11`, `33.0`, `33`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/33/apache/Dockerfile)

-	[`33.0.11-fpm`, `33.0-fpm`, `33-fpm`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/33/fpm/Dockerfile)

-	[`33.0.11-fpm-alpine`, `33.0-fpm-alpine`, `33-fpm-alpine`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/33/fpm-alpine/Dockerfile)

-	[`34.0.6-apache`, `34.0-apache`, `34-apache`, `stable-apache`, `production-apache`, `34.0.6`, `34.0`, `34`, `stable`, `production`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/34/apache/Dockerfile)

-	[`34.0.6-fpm`, `34.0-fpm`, `34-fpm`, `stable-fpm`, `production-fpm`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/34/fpm/Dockerfile)

-	[`34.0.6-fpm-alpine`, `34.0-fpm-alpine`, `34-fpm-alpine`, `stable-fpm-alpine`, `production-fpm-alpine`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/34/fpm-alpine/Dockerfile)

-	[`35.0.3-apache`, `35.0-apache`, `35-apache`, `apache`, `35.0.3`, `35.0`, `35`, `latest`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/35/apache/Dockerfile)

-	[`35.0.3-fpm`, `35.0-fpm`, `35-fpm`, `fpm`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/35/fpm/Dockerfile)

-	[`35.0.3-fpm-alpine`, `35.0-fpm-alpine`, `35-fpm-alpine`, `fpm-alpine`](https://github.com/nextcloud/docker/blob/a6918807cb013715d2ac9477f757df4ce5f2cef5/35/fpm-alpine/Dockerfile)

# Quick reference (cont.)

-	**Where to file issues**:  
	[https://github.com/nextcloud/docker/issues](https://github.com/nextcloud/docker/issues?q=is:issue+is:pr)

-	**Supported architectures**: ([more info](https://github.com/docker-library/official-images#architectures-other-than-amd64))  
	[`amd64`](https://hub.docker.com/r/amd64/nextcloud/), [`arm32v5`](https://hub.docker.com/r/arm32v5/nextcloud/), [`arm32v6`](https://hub.docker.com/r/arm32v6/nextcloud/), [`arm32v7`](https://hub.docker.com/r/arm32v7/nextcloud/), [`arm64v8`](https://hub.docker.com/r/arm64v8/nextcloud/), [`i386`](https://hub.docker.com/r/i386/nextcloud/), [`ppc64le`](https://hub.docker.com/r/ppc64le/nextcloud/), [`riscv64`](https://hub.docker.com/r/riscv64/nextcloud/), [`s390x`](https://hub.docker.com/r/s390x/nextcloud/)

-	**Published image artifact details**:  
	[repo-info repo's `repos/nextcloud/` directory](https://github.com/docker-library/repo-info/blob/master/repos/nextcloud) ([history](https://github.com/docker-library/repo-info/commits/master/repos/nextcloud))  
	(image metadata, transfer size, etc)

-	**Image updates**:  
	[official-images repo's `library/nextcloud` label](https://github.com/docker-library/official-images/issues?q=label%3Alibrary%2Fnextcloud)  
	[official-images repo's `library/nextcloud` file](https://github.com/docker-library/official-images/blob/master/library/nextcloud) ([history](https://github.com/docker-library/official-images/commits/master/library/nextcloud))

-	**Source of this description**:  
	[docs repo's `nextcloud/` directory](https://github.com/docker-library/docs/tree/master/nextcloud) ([history](https://github.com/docker-library/docs/commits/master/nextcloud))

# What is Nextcloud?

A safe home for all your data. Access & share your files, calendars, contacts, mail & more from any device, on your terms.

> [Nextcloud.com](https://nextcloud.com/)

This Docker micro-service image is developed and maintained by the Nextcloud community. Nextcloud GmbH does not offer support for this Docker image. When you are looking to get professional support, you can become an [enterprise](https://nextcloud.com/enterprise/) customer or use [AIO](https://github.com/nextcloud/all-in-one#nextcloud-all-in-one).

# How to use this image

This image is designed to be used in a micro-service environment. There are two versions of the image you can choose from.

The `apache` tag contains a full Nextcloud installation including an apache web server. It is designed to be easy to use and gets you running pretty fast. This is also the default for the `latest` tag and version tags that are not further specified.

The second option is a `fpm` container. It is based on the [php-fpm](https://hub.docker.com/_/php/) image and runs a fastCGI-Process that serves your Nextcloud page. To use this image it must be combined with any webserver that can proxy the http requests to the FastCGI-port of the container.

## Using the apache image

The apache image contains a webserver and exposes port 80. To start the container type:

```console
$ docker run -d -p 8080:80 nextcloud
```

Now you can access Nextcloud at http://localhost:8080/ from your host system.

## Using the fpm image

To use the fpm image, you need an additional web server, such as [nginx](https://docs.nextcloud.com/server/latest/admin_manual/installation/nginx.html), that can proxy http-request to the fpm-port of the container. For fpm connection this container exposes port 9000. In most cases, you might want to use another container or your host as proxy. If you use your host you can address your Nextcloud container directly on port 9000. If you use another container, make sure that you add them to the same docker network (via `docker run --network <NAME> ...` or a `compose.yaml` file). In both cases you don't want to map the fpm port to your host.

```console
$ docker run -d nextcloud:fpm
```

As the fastCGI-Process is not capable of serving static files (style sheets, images, ...), the webserver needs access to these files. This can be achieved with the `volumes-from` option. You can find more information in the Docker Compose section.

## Using an external database

By default, this container uses SQLite for data storage but the Nextcloud setup wizard (appears on first run) allows connecting to an existing MySQL/MariaDB or PostgreSQL database. You can also link a database container, e. g. `--link my-mysql:mysql`, and then use `mysql` as the database host on setup. More info is in the Docker Compose section.

## Persistent data

The Nextcloud installation and all data beyond what lives in the database (file uploads, etc.) are stored in the [unnamed docker volume](https://docs.docker.com/storage/volumes/) volume `/var/www/html`. The docker daemon will store that data within the docker directory `/var/lib/docker/volumes/...`. That means your data is saved even if the container crashes, is stopped or deleted.

A named Docker volume or a mounted host directory should be used for upgrades and backups. To achieve this, you need one volume for your database container and one for Nextcloud.

Nextcloud:

-	`/var/www/html/` folder where all Nextcloud data lives

```console
$ docker run -d \
-v nextcloud:/var/www/html \
nextcloud
```

Database:

-	`/var/lib/mysql` MySQL / MariaDB Data
-	`/var/lib/postgresql/data` PostgreSQL Data

```console
$ docker run -d \
-v db:/var/lib/mysql \
mariadb:10.6
```

### Additional volumes

If you want to get fine grained access to your individual files, you can mount additional volumes for data, config, your theme and custom apps. The `data`, `config` files are stored in respective subfolders inside `/var/www/html/`. The apps are split into core `apps` (which are shipped with Nextcloud and you don't need to take care of) and a `custom_apps` folder. If you use a custom theme it would go into the `themes` subfolder.

Overview of the folders that can be mounted as volumes:

-	`/var/www/html` Main folder, needed for updating
-	`/var/www/html/custom_apps` installed / modified apps
-	`/var/www/html/config` local configuration
-	`/var/www/html/data` the actual data of your Nextcloud
-	`/var/www/html/themes/<YOUR_CUSTOM_THEME>` theming/branding

If you want to use named volumes for all of these, it would look like this:

```console
$ docker run -d \
-v nextcloud:/var/www/html \
-v apps:/var/www/html/custom_apps \
-v config:/var/www/html/config \
-v data:/var/www/html/data \
-v theme:/var/www/html/themes/<YOUR_CUSTOM_THEME> \
nextcloud
```

### Custom volumes

If mounting additional volumes under `/var/www/html`, you should consider:

-	Confirming that [upgrade.exclude](https://github.com/nextcloud/docker/blob/master/upgrade.exclude) contains the files and folders that should persist during installation and upgrades; or
-	Mounting storage volumes to locations outside of `/var/www/html`.

> You should note that data inside the main folder (`/var/www/html`) will be overridden/removed during installation and upgrades, unless listed in [upgrade.exclude](https://github.com/nextcloud/docker/blob/master/upgrade.exclude). The additional volumes officially supported are already in that list, but custom volumes will need to be added by you. We suggest mounting custom storage volumes outside of `/var/www/html` and if possible read-only so that making this adjustment is unnecessary. If you must do so, however, you may build a custom image with a modified `/upgrade.exclude` file that incorporates your custom volume(s).

## Using the Nextcloud command-line interface

To use the [Nextcloud command-line interface](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/occ_command.html) (aka. `occ` command):

```console
$ docker exec --user www-data CONTAINER_ID php occ
```

or for `docker compose`:

```console
$ docker compose exec --user www-data app php occ
```

## Auto configuration via environment variables

The Nextcloud image supports auto configuration via environment variables. You can preconfigure everything that is asked on the install page on first run. To enable auto configuration, set your database connection via the following environment variables. You must specify all of the environment variables for a given database or the database environment variables defaults to SQLITE. ONLY use one database type!

**SQLite**:

-	`SQLITE_DATABASE` Name of the database using sqlite

**MYSQL/MariaDB**:

-	`MYSQL_DATABASE` Name of the database using mysql / mariadb.
-	`MYSQL_USER` Username for the database using mysql / mariadb.
-	`MYSQL_PASSWORD` Password for the database user using mysql / mariadb.
-	`MYSQL_HOST` Hostname of the database server using mysql / mariadb.

**PostgreSQL**:

-	`POSTGRES_DB` Name of the database using postgres.
-	`POSTGRES_USER` Username for the database using postgres.
-	`POSTGRES_PASSWORD` Password for the database user using postgres.
-	`POSTGRES_HOST` Hostname of the database server using postgres.

As an alternative to passing sensitive information via environment variables, `_FILE` may be appended to the previously listed environment variables, causing the initialization script to load the values for those variables from files present in the container. See [Docker secrets](#docker-secrets) section below.

If you set any group of values (i.e. all of `MYSQL_DATABASE`, `MYSQL_USER`, `MYSQL_PASSWORD`, `MYSQL_HOST`), they will not be asked in the install page on first run. With a complete configuration by using all variables for your database type, you can additionally configure your Nextcloud instance by setting admin user and password (only works if you set both):

-	`NEXTCLOUD_ADMIN_USER` Name of the Nextcloud admin user.
-	`NEXTCLOUD_ADMIN_PASSWORD` Password for the Nextcloud admin user.

If you want, you can set the data directory, otherwise default value will be used.

-	`NEXTCLOUD_DATA_DIR` (default: `/var/www/html/data`) Configures the data directory where nextcloud stores all files from the users.

One or more trusted domains can be set through environment variable, too. They will be added to the configuration after install.

-	`NEXTCLOUD_TRUSTED_DOMAINS` (not set by default) Optional space-separated list of domains

The install and update script is only triggered when a default command is used (`apache-foreground` or `php-fpm`). If you use a custom command you have to enable the install / update with

-	`NEXTCLOUD_UPDATE` (default: `0`)

You might want to make sure the htaccess is up to date after each container update. Especially on multiple swarm nodes as any discrepancy will make your server unusable.

-	`NEXTCLOUD_INIT_HTACCESS` (not set by default) Set it to true to enable run `occ maintenance:update:htaccess` after container initialization.

If you want to use Redis you have to create a separate [Redis](https://hub.docker.com/_/redis/) container in your setup / in your Compose file. To inform Nextcloud about the Redis container, pass in the following parameters:

-	`REDIS_HOST` (not set by default) Name of Redis container
-	`REDIS_HOST_PORT` (default: `6379`) Optional port for Redis, only use for external Redis servers that run on non-standard ports.
-	`REDIS_HOST_PASSWORD` (not set by default) Redis password

The use of Redis is recommended to prevent file locking problems. See the examples for further instructions.

To use an external SMTP server, you have to provide the connection details. To configure Nextcloud to use SMTP add:

-	`SMTP_HOST` (not set by default): The hostname of the SMTP server.
-	`SMTP_SECURE` (empty by default): Set to `ssl` to use SSL, or `tls` to use STARTTLS.
-	`SMTP_PORT` (default: `465` for SSL and `25` for non-secure connections): Optional port for the SMTP connection. Use `587` for an alternative port for STARTTLS.
-	`SMTP_AUTHTYPE` (default: `LOGIN`): The method used for authentication. Use `PLAIN` if no authentication is required.
-	`SMTP_NAME` (empty by default): The username for the authentication.
-	`SMTP_PASSWORD` (empty by default): The password for the authentication.
-	`MAIL_FROM_ADDRESS` (not set by default): Set the local-part for the 'from' field in the emails sent by Nextcloud.
-	`MAIL_DOMAIN` (not set by default): Set a different domain for the emails than the domain where Nextcloud is installed.

At least `SMTP_HOST`, `MAIL_FROM_ADDRESS` and `MAIL_DOMAIN` must be set for the configurations to be applied.

Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/email_configuration.html) for other values to configure SMTP.

To use an external S3 compatible object store as primary storage, set the following variables:

-	`OBJECTSTORE_S3_BUCKET`: The name of the bucket that Nextcloud should store the data in
-	`OBJECTSTORE_S3_REGION`: The region that the S3 bucket resides in
-	`OBJECTSTORE_S3_HOST`: The hostname of the object storage server
-	`OBJECTSTORE_S3_PORT`: The port that the object storage server is being served over
-	`OBJECTSTORE_S3_KEY`: AWS style access key
-	`OBJECTSTORE_S3_SECRET`: AWS style secret access key
-	`OBJECTSTORE_S3_STORAGE_CLASS`: The storage class to use when adding objects to the bucket
-	`OBJECTSTORE_S3_SSL` (default: `true`): Whether or not SSL/TLS should be used to communicate with object storage server
-	`OBJECTSTORE_S3_USEPATH_STYLE` (default: `false`): Not required for AWS S3
-	`OBJECTSTORE_S3_LEGACYAUTH` (default: `false`): Not required for AWS S3
-	`OBJECTSTORE_S3_OBJECT_PREFIX` (default: `urn:oid:`): Prefix to prepend to the fileid
-	`OBJECTSTORE_S3_AUTOCREATE` (default: `true`): Create the container if it does not exist
-	`OBJECTSTORE_S3_SSE_C_KEY` (not set by default): Base64 encoded key with a maximum length of 32 bytes for server side encryption (SSE-C)

Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_files/primary_storage.html#simple-storage-service-s3) for more information.

To use an external OpenStack Swift object store as primary storage, set the following variables:

-	`OBJECTSTORE_SWIFT_URL`: The Swift identity (Keystone) endpoint
-	`OBJECTSTORE_SWIFT_AUTOCREATE` (default: `false`): Whether or not Nextcloud should automatically create the Swift container
-	`OBJECTSTORE_SWIFT_USER_NAME`: Swift username
-	`OBJECTSTORE_SWIFT_USER_PASSWORD`: Swift user password
-	`OBJECTSTORE_SWIFT_USER_DOMAIN` (default: `Default`): Swift user domain
-	`OBJECTSTORE_SWIFT_PROJECT_NAME`: OpenStack project name
-	`OBJECTSTORE_SWIFT_PROJECT_DOMAIN` (default: `Default`): OpenStack project domain
-	`OBJECTSTORE_SWIFT_SERVICE_NAME` (default: `swift`): Swift service name
-	`OBJECTSTORE_SWIFT_REGION`: Swift endpoint region
-	`OBJECTSTORE_SWIFT_CONTAINER_NAME`: Swift container (bucket) that Nextcloud should store the data in

Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_files/primary_storage.html#openstack-swift) for more information.

To customize other PHP limits you can simply change the following variables:

-	`PHP_MEMORY_LIMIT` (default `512M`) This sets the maximum amount of memory in bytes that a script is allowed to allocate. This is meant to help prevent poorly written scripts from eating up all available memory but it can prevent normal operation if set too tight.
-	`PHP_UPLOAD_LIMIT` (default `512M`) This sets the upload limit (`post_max_size` and `upload_max_filesize`) for big files. Note that you may have to change other limits depending on your client, webserver or operating system. Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_files/big_file_upload_configuration.html) for more information.

To customize Apache max file upload limit you can change the following variable:

-	`APACHE_BODY_LIMIT` (default `1073741824` [1GiB]) This restricts the total size of the HTTP request body sent from the client. It specifies the number of *bytes* that are allowed in a request body. A value of **0** means **unlimited**. Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_files/big_file_upload_configuration.html#apache) for more information.

## Auto configuration via hook folders

There are 5 hooks

-	`pre-installation` Executed before the Nextcloud is installed/initiated
-	`post-installation` Executed after the Nextcloud is installed/initiated
-	`pre-upgrade` Executed before the Nextcloud is upgraded
-	`post-upgrade` Executed after the Nextcloud is upgraded
-	`before-starting` Executed before the Nextcloud starts

To use the hooks triggered by the `entrypoint` script, either

-	Added your script(s) to the individual of the hook folder(s), which are located at the path `/docker-entrypoint-hooks.d` in the container
-	Use volume(s) if you want to use script from the host system inside the container, see example.

**Note:** Only the script(s) located in a hook folder (not sub-folders), ending with `.sh` and marked as executable, will be executed.

**Example:** Mount using volumes

```yaml
...
  app:
    image: nextcloud:stable

    volumes:
      - ./app-hooks/pre-installation:/docker-entrypoint-hooks.d/pre-installation
      - ./app-hooks/post-installation:/docker-entrypoint-hooks.d/post-installation
      - ./app-hooks/pre-upgrade:/docker-entrypoint-hooks.d/pre-upgrade
      - ./app-hooks/post-upgrade:/docker-entrypoint-hooks.d/post-upgrade
      - ./app-hooks/before-starting:/docker-entrypoint-hooks.d/before-starting
...
```

## Using the apache image behind a reverse proxy and auto configure server host and protocol

The apache image will replace the remote addr (IP address visible to Nextcloud) with the IP address from `X-Real-IP` if the request is coming from a proxy in `10.0.0.0/8`, `172.16.0.0/12` or `192.168.0.0/16` by default. If you want Nextcloud to pick up the server host (`HTTP_X_FORWARDED_HOST`), protocol (`HTTP_X_FORWARDED_PROTO`) and client IP (`HTTP_X_FORWARDED_FOR`) from a trusted proxy, then disable rewrite IP and add the reverse proxy's IP address to `TRUSTED_PROXIES`.

-	`APACHE_DISABLE_REWRITE_IP` (not set by default): Set to 1 to disable rewrite IP.
-	`TRUSTED_PROXIES` (empty by default): A space-separated list of trusted proxies. CIDR notation is supported for IPv4.

If the `TRUSTED_PROXIES` approach does not work for you, try using fixed values for overwrite parameters.

-	`OVERWRITEHOST` (empty by default): Set the hostname of the proxy. Can also specify a port.
-	`OVERWRITEPROTOCOL` (empty by default): Set the protocol of the proxy, http or https.
-	`OVERWRITECLIURL` (empty by default): Set the cli url of the proxy (e.g. https://mydnsname.example.com)
-	`OVERWRITEWEBROOT` (empty by default): Set the absolute path of the proxy.
-	`OVERWRITECONDADDR` (empty by default): Regex to overwrite the values dependent on the remote address.

Check the [Nexcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/reverse_proxy_configuration.html) for more details.

Keep in mind that once set, removing these environment variables won't remove these values from the configuration file, due to how Nextcloud merges configuration files together.

# Running this image with Docker Compose

The easiest way to get a fully featured and functional setup is using a `compose.yaml` file. There are too many different possibilities to setup your system, so here are only some examples of what you have to look for.

At first, make sure you have chosen the right base image (fpm or apache) and added features you wanted (see below). In every case, you would want to add a database container and docker volumes to get easy access to your persistent data. When you want to have your server reachable from the internet, adding HTTPS-encryption is mandatory! See below for more information.

## Base version - apache

This version will use the apache image and add a mariaDB container. The volumes are set to keep your data persistent. This setup provides **no ssl encryption** and is intended to run behind a proxy.

Make sure to pass in values for `MYSQL_ROOT_PASSWORD` and `MYSQL_PASSWORD` variables before you run this setup.

```yaml
volumes:
  nextcloud:
  db:

services:
  db:
    image: mariadb:10.6
    restart: always
    command: --transaction-isolation=READ-COMMITTED --log-bin=binlog --binlog-format=ROW
    volumes:
      - db:/var/lib/mysql
    environment:
      - MYSQL_ROOT_PASSWORD=
      - MYSQL_PASSWORD=
      - MYSQL_DATABASE=nextcloud
      - MYSQL_USER=nextcloud

  app:
    image: nextcloud
    restart: always
    ports:
      - 8080:80
    links:
      - db
    volumes:
      - nextcloud:/var/www/html
    environment:
      - MYSQL_PASSWORD=
      - MYSQL_DATABASE=nextcloud
      - MYSQL_USER=nextcloud
      - MYSQL_HOST=db
```

Then run `docker compose up -d`, now you can access Nextcloud at http://localhost:8080/ from your host system.

## Base version - FPM

...

**Note:** the description for this image is longer than the Hub length limit of 25000, so has been trimmed.  The full description can be found at [https://github.com/docker-library/docs/tree/master/nextcloud/README.md](https://github.com/docker-library/docs/tree/master/nextcloud/README.md).  See also [docker/hub-feedback#238](https://github.com/docker/hub-feedback/issues/238) and [docker/roadmap#475](https://github.com/docker/roadmap/issues/475).