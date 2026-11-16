<!-- source: https://hub.docker.com/_/nextcloud fetched 2026-11-15, extract -->

```console
$ docker run -d -p 8080:80 nextcloud
```

To use the fpm image, you need an additional web server, such as [nginx](https://docs.nextcloud.com/server/latest/admin_manual/installation/nginx.html), that can proxy http-request to the fpm-port of the container. For fpm connection this container exposes port 9000. In most cases, you might want to use another container or your host as proxy. If you use your host you can address your Nextcloud container directly on port 9000. If you use another container, make sure that you add them to the same docker network (via `docker run --network <NAME> ...` or a `compose.yaml` file). In both cases you don't want to map the fpm port to your host.

```console
$ docker run -d nextcloud:fpm
```

```console
$ docker run -d \
-v nextcloud:/var/www/html \
nextcloud
```

```console
$ docker run -d \
-v db:/var/lib/mysql \
mariadb:10.6
```

-	`/var/www/html/themes/<YOUR_CUSTOM_THEME>` theming/branding

```console
$ docker run -d \
-v nextcloud:/var/www/html \
-v apps:/var/www/html/custom_apps \
-v config:/var/www/html/config \
-v data:/var/www/html/data \
-v theme:/var/www/html/themes/<YOUR_CUSTOM_THEME> \
nextcloud
```

## Auto configuration via environment variables

-	`SQLITE_DATABASE` Name of the database using sqlite

-	`MYSQL_DATABASE` Name of the database using mysql / mariadb.
-	`MYSQL_USER` Username for the database using mysql / mariadb.
-	`MYSQL_PASSWORD` Password for the database user using mysql / mariadb.
-	`MYSQL_HOST` Hostname of the database server using mysql / mariadb.

-	`POSTGRES_DB` Name of the database using postgres.
-	`POSTGRES_USER` Username for the database using postgres.
-	`POSTGRES_PASSWORD` Password for the database user using postgres.
-	`POSTGRES_HOST` Hostname of the database server using postgres.

If you set any group of values (i.e. all of `MYSQL_DATABASE`, `MYSQL_USER`, `MYSQL_PASSWORD`, `MYSQL_HOST`), they will not be asked in the install page on first run. With a complete configuration by using all variables for your database type, you can additionally configure your Nextcloud instance by setting admin user and password (only works if you set both):

-	`NEXTCLOUD_ADMIN_USER` Name of the Nextcloud admin user.
-	`NEXTCLOUD_ADMIN_PASSWORD` Password for the Nextcloud admin user.

-	`NEXTCLOUD_DATA_DIR` (default: `/var/www/html/data`) Configures the data directory where nextcloud stores all files from the users.

-	`NEXTCLOUD_TRUSTED_DOMAINS` (not set by default) Optional space-separated list of domains

-	`NEXTCLOUD_UPDATE` (default: `0`)

-	`NEXTCLOUD_INIT_HTACCESS` (not set by default) Set it to true to enable run `occ maintenance:update:htaccess` after container initialization.

-	`REDIS_HOST` (not set by default) Name of Redis container
-	`REDIS_HOST_PORT` (default: `6379`) Optional port for Redis, only use for external Redis servers that run on non-standard ports.
-	`REDIS_HOST_PASSWORD` (not set by default) Redis password

-	`SMTP_HOST` (not set by default): The hostname of the SMTP server.
-	`SMTP_SECURE` (empty by default): Set to `ssl` to use SSL, or `tls` to use STARTTLS.
-	`SMTP_PORT` (default: `465` for SSL and `25` for non-secure connections): Optional port for the SMTP connection. Use `587` for an alternative port for STARTTLS.
-	`SMTP_AUTHTYPE` (default: `LOGIN`): The method used for authentication. Use `PLAIN` if no authentication is required.
-	`SMTP_NAME` (empty by default): The username for the authentication.
-	`SMTP_PASSWORD` (empty by default): The password for the authentication.
-	`MAIL_FROM_ADDRESS` (not set by default): Set the local-part for the 'from' field in the emails sent by Nextcloud.
-	`MAIL_DOMAIN` (not set by default): Set a different domain for the emails than the domain where Nextcloud is installed.

At least `SMTP_HOST`, `MAIL_FROM_ADDRESS` and `MAIL_DOMAIN` must be set for the configurations to be applied.

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

-	`PHP_MEMORY_LIMIT` (default `512M`) This sets the maximum amount of memory in bytes that a script is allowed to allocate. This is meant to help prevent poorly written scripts from eating up all available memory but it can prevent normal operation if set too tight.
-	`PHP_UPLOAD_LIMIT` (default `512M`) This sets the upload limit (`post_max_size` and `upload_max_filesize`) for big files. Note that you may have to change other limits depending on your client, webserver or operating system. Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_files/big_file_upload_configuration.html) for more information.

-	`APACHE_BODY_LIMIT` (default `1073741824` [1GiB]) This restricts the total size of the HTTP request body sent from the client. It specifies the number of *bytes* that are allowed in a request body. A value of **0** means **unlimited**. Check the [Nextcloud documentation](https://docs.nextcloud.com/server/latest/admin_manual/configuration_files/big_file_upload_configuration.html#apache) for more information.

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

The apache image will replace the remote addr (IP address visible to Nextcloud) with the IP address from `X-Real-IP` if the request is coming from a proxy in `10.0.0.0/8`, `172.16.0.0/12` or `192.168.0.0/16` by default. If you want Nextcloud to pick up the server host (`HTTP_X_FORWARDED_HOST`), protocol (`HTTP_X_FORWARDED_PROTO`) and client IP (`HTTP_X_FORWARDED_FOR`) from a trusted proxy, then disable rewrite IP and add the reverse proxy's IP address to `TRUSTED_PROXIES`.

-	`APACHE_DISABLE_REWRITE_IP` (not set by default): Set to 1 to disable rewrite IP.
-	`TRUSTED_PROXIES` (empty by default): A space-separated list of trusted proxies. CIDR notation is supported for IPv4.

If the `TRUSTED_PROXIES` approach does not work for you, try using fixed values for overwrite parameters.

-	`OVERWRITEHOST` (empty by default): Set the hostname of the proxy. Can also specify a port.
-	`OVERWRITEPROTOCOL` (empty by default): Set the protocol of the proxy, http or https.
-	`OVERWRITECLIURL` (empty by default): Set the cli url of the proxy (e.g. https://mydnsname.example.com)
-	`OVERWRITEWEBROOT` (empty by default): Set the absolute path of the proxy.
-	`OVERWRITECONDADDR` (empty by default): Regex to overwrite the values dependent on the remote address.

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

