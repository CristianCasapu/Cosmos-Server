<!-- source: https://hub.docker.com/_/redis fetched 2026-11-15, extract -->

By default, the Redis Docker image drops privileges by switching to the redis user and removing unnecessary capabilities. This step is skipped if Docker is run with the `--user` option or if you set the `SKIP_DROP_PRIVS=1` (since 8.0.2) environment variable.

Note: Using `SKIP_DROP_PRIVS` is not recommended, as it reduces the container's security.

```console
$ docker run --name some-redis -d redis
```

```console
$ docker run --name some-redis -d redis redis-server --save 60 1 --loglevel warning
```

You can skip this step by setting the `SKIP_FIX_PERMS=1`(since 8.0.2) environment variable.

```console
$ docker run --rm -v /your/host/path:/data redis chown -R redis:redis /data
```

```console
$ docker run -it --network some-network --rm redis redis-cli -h some-redis
```

```console
$ docker run -v /myredis/conf:/usr/local/etc/redis --name myredis redis redis-server /usr/local/etc/redis/redis.conf
```

```console
$ docker run -v /myredis/conf:/usr/local/etc/redis --name myredis redis:8.12-rc2 redis-server /usr/local/etc/redis/redis.conf --config-rewrite-source-aware yes
```

