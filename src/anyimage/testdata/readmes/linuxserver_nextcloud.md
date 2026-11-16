<!-- source: https://hub.docker.com/r/linuxserver/nextcloud fetched 2026-11-15, extract -->

* easy user mappings (PGID, PUID)

```yaml
---
services:
  nextcloud:
    image: lscr.io/linuxserver/nextcloud:latest
    container_name: nextcloud
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Etc/UTC
    volumes:
      - /path/to/nextcloud/config:/config
      - /path/to/data:/data
    ports:
      - 443:443
    restart: unless-stopped
```

```bash
docker run -d \
  --name=nextcloud \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Etc/UTC \
  -p 443:443 \
  -v /path/to/nextcloud/config:/config \
  -v /path/to/data:/data \
  --restart unless-stopped \
  lscr.io/linuxserver/nextcloud:latest
```

| Parameter | Function |
| :----: | --- |
| `-e PUID=1000` | for UserID - see below for explanation |
| `-e PGID=1000` | for GroupID - see below for explanation |
| `-e TZ=Etc/UTC` | specify a timezone to use, see this [list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones#List). |

You can set any environment variable from a file by using a special prepend `FILE__`.

For all of our images we provide the ability to override the default umask settings for services started within the containers using the optional `-e UMASK=022` setting.

When using volumes (`-v` flags), permissions issues can arise between the host OS and the container, we avoid this issue by allowing you to specify the user `PUID` and group `PGID`.

In this instance `PUID=1000` and `PGID=1000`, to find yours use `id your_user` as below:

* Recreate a new container with the same docker run parameters as instructed above (if mapped correctly to a host folder, your `/config` folder and settings will be preserved)

```bash
docker run --rm --privileged lscr.io/linuxserver/qemu-static --reset
```

