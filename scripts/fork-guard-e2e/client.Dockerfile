# The home and outside clients and the router of scripts/fork-guard-e2e.sh.
FROM debian:13
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-client curl netcat-openbsd iproute2 \
      shellcheck busybox procps \
 && rm -rf /var/lib/apt/lists/*
