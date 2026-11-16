<!-- source: https://hub.docker.com/r/vaultwarden/server fetched 2026-11-15, extract -->

```sh
docker pull vaultwarden/server:latest
docker run -d --name vaultwarden -v /vw-data/:/data/ -p 80:80 vaultwarden/server:latest
```
