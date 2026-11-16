<!-- source: https://hub.docker.com/r/linuxserver/qbittorrent fetched 2026-11-15, extract -->

* easy user mappings (PGID, PUID)

### WEBUI_PORT variable

Due to issues with CSRF and port mapping, should you require to alter the port for the web UI you need to change both sides of the -p 8080 switch AND set the WEBUI_PORT variable to the new port.

For example, to set the port to 8123 you need to set -p 8123:8123 and -e WEBUI_PORT=8123

### TORRENTING_PORT

Similarly to the WEBUI_PORT, to set the port to 6887 you need to pass -p 6887:6887, -p 6887:6887/udp and -e TORRENTING_PORT=6887 arguments to Docker.

```yaml
---
services:
  qbittorrent:
    image: lscr.io/linuxserver/qbittorrent:latest
    container_name: qbittorrent
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Etc/UTC
      - WEBUI_PORT=8080
      - TORRENTING_PORT=6881
    volumes:
      - /path/to/qbittorrent/appdata:/config
      - /path/to/downloads:/downloads #optional
    ports:
      - 8080:8080
      - 6881:6881
      - 6881:6881/udp
    stop_grace_period: "10s" #optional
    restart: unless-stopped
```

```bash
docker run -d \
  --name=qbittorrent \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Etc/UTC \
  -e WEBUI_PORT=8080 \
  -e TORRENTING_PORT=6881 \
  -p 8080:8080 \
  -p 6881:6881 \
  -p 6881:6881/udp \
  -v /path/to/qbittorrent/appdata:/config \
  -v /path/to/downloads:/downloads `#optional` \
  --stop-timeout="10s" `#optional` \
  --restart unless-stopped \
  lscr.io/linuxserver/qbittorrent:latest
```

| Parameter | Function |
| :----: | --- |
| `-e PUID=1000` | for UserID - see below for explanation |
| `-e PGID=1000` | for GroupID - see below for explanation |
| `-e TZ=Etc/UTC` | specify a timezone to use, see this [list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones#List). |
| `-e WEBUI_PORT=8080` | for changing the port of the web UI, see below for explanation |
| `-e TORRENTING_PORT=6881` | for changing the port of tcp/udp connection, see below for explanation |

You can set any environment variable from a file by using a special prepend `FILE__`.

For all of our images we provide the ability to override the default umask settings for services started within the containers using the optional `-e UMASK=022` setting.

When using volumes (`-v` flags), permissions issues can arise between the host OS and the container, we avoid this issue by allowing you to specify the user `PUID` and group `PGID`.

In this instance `PUID=1000` and `PGID=1000`, to find yours use `id your_user` as below:

* Recreate a new container with the same docker run parameters as instructed above (if mapped correctly to a host folder, your `/config` folder and settings will be preserved)

```bash
docker run --rm --privileged lscr.io/linuxserver/qemu-static --reset
```

* **20.01.21:** - Deprecate `UMASK_SET` in favor of UMASK in baseimage, see above for more information.
* **16.09.17:** - Bump to 3.3.16, Add WEBUI_PORT variable and notes to README to allow changing port of webui.
