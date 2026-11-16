<!-- source: https://hub.docker.com/r/linuxserver/mariadb fetched 2026-11-15, extract -->

* easy user mappings (PGID, PUID)

NOTE if you want to use (MYSQL_DATABASE MYSQL_USER MYSQL_PASSWORD) **all three** of these variables need to be set you cannot pick and choose.

The `MYSQL_ROOT_PASSWORD MYSQL_DATABASE MYSQL_USER MYSQL_PASSWORD REMOTE_SQL` env values can be set in a file:

This will have the same effect as setting the `REMOTE_SQL` environment variable. The sql will only be run on the containers first boot and setup.

When this container initializes, if `MYSQL_ROOT_PASSWORD` is set an upgrade check will run. If an upgrade is required the log will indicate the need stop any services that are accessing databases in this container, and then run the command:

```yaml
---
services:
  mariadb:
    image: lscr.io/linuxserver/mariadb:latest
    container_name: mariadb
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Etc/UTC
      - MYSQL_ROOT_PASSWORD=ROOT_ACCESS_PASSWORD
      - MYSQL_DATABASE=USER_DB_NAME #optional
      - MYSQL_USER=MYSQL_USER #optional
      - MYSQL_PASSWORD=DATABASE_PASSWORD #optional
      - REMOTE_SQL=http://URL1/your.sql,https://URL2/your.sql #optional
      - CLI_OPTS= #optional
    volumes:
      - /path/to/mariadb/config:/config
    ports:
      - 3306:3306
    restart: unless-stopped
```

```bash
docker run -d \
  --name=mariadb \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Etc/UTC \
  -e MYSQL_ROOT_PASSWORD=ROOT_ACCESS_PASSWORD \
  -e MYSQL_DATABASE=USER_DB_NAME `#optional` \
  -e MYSQL_USER=MYSQL_USER `#optional` \
  -e MYSQL_PASSWORD=DATABASE_PASSWORD `#optional` \
  -e REMOTE_SQL=http://URL1/your.sql,https://URL2/your.sql `#optional` \
  -e CLI_OPTS= `#optional` \
  -p 3306:3306 \
  -v /path/to/mariadb/config:/config \
  --restart unless-stopped \
  lscr.io/linuxserver/mariadb:latest
```

| Parameter | Function |
| :----: | --- |
| `-e PUID=1000` | for UserID - see below for explanation |
| `-e PGID=1000` | for GroupID - see below for explanation |
| `-e TZ=Etc/UTC` | specify a timezone to use, see this [list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones#List). |
| `-e MYSQL_ROOT_PASSWORD=ROOT_ACCESS_PASSWORD` | Set this to root password for installation (minimum 4 characters & non-alphanumeric passwords must be properly escaped). (valid only for first run) |
| `-e MYSQL_DATABASE=USER_DB_NAME` | Specify the name of a database to be created. (valid only for first run) |
| `-e MYSQL_USER=MYSQL_USER` | This user will have superuser access to the database specified by MYSQL_DATABASE (do not use root here). (valid only for first run) |
| `-e MYSQL_PASSWORD=DATABASE_PASSWORD` | Set this to the password you want to use for you MYSQL_USER (minimum 4 characters & non-alphanumeric passwords must be properly escaped). (valid only for first run) |
| `-e REMOTE_SQL=http://URL1/your.sql,https://URL2/your.sql` | Set this to ingest sql files from an http/https endpoint (comma seperated array). |
| `-e CLI_OPTS=` | Pass CLI options to mariadbd via mariadbd-safe. Use with caution as mariadbd option precedence is complex. |

You can set any environment variable from a file by using a special prepend `FILE__`.

For all of our images we provide the ability to override the default umask settings for services started within the containers using the optional `-e UMASK=022` setting.

When using volumes (`-v` flags), permissions issues can arise between the host OS and the container, we avoid this issue by allowing you to specify the user `PUID` and group `PGID`.

In this instance `PUID=1000` and `PGID=1000`, to find yours use `id your_user` as below:

* Recreate a new container with the same docker run parameters as instructed above (if mapped correctly to a host folder, your `/config` folder and settings will be preserved)

```bash
docker run --rm --privileged lscr.io/linuxserver/qemu-static --reset
```

