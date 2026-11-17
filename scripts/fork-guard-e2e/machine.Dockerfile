# A test machine for scripts/fork-guard-e2e.sh: systemd, sshd, Docker, and the
# packages the protection pieces install, downloaded but not installed (the test
# networks have no way out: apt-get installs them from the cache).
# DOCKER=distro takes the distribution's docker.io; DOCKER=ce Docker 29 from
# download.docker.com (as on the owner's server).
ARG BASE=debian:13
FROM ${BASE}
ARG DOCKER=ce
ENV DEBIAN_FRONTEND=noninteractive
RUN rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv dbus openssh-server openssh-client \
      iproute2 busybox curl ca-certificates netcat-openbsd procps kmod sudo \
      conntrack util-linux tmux screen gnupg \
 && if [ "$DOCKER" = ce ]; then \
      . /etc/os-release \
      && install -m 0755 -d /etc/apt/keyrings \
      && curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc \
      && echo "deb [signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" > /etc/apt/sources.list.d/docker.list \
      && apt-get update \
      && apt-get install -y --no-install-recommends 'docker-ce=5:29.*' 'docker-ce-cli=5:29.*' containerd.io iptables \
      && echo docker-ce > /etc/test-docker-package; \
    else \
      apt-get install -y --no-install-recommends docker.io \
      && (apt-get install -y --no-install-recommends docker-cli || true) \
      && echo docker.io > /etc/test-docker-package; \
    fi \
 && apt-get install -y -d --no-install-recommends nftables fail2ban python3 unattended-upgrades \
 && apt-get install -y -d --no-install-recommends ufw \
 && apt-get install -y -d --no-install-recommends dropbear-bin \
 && apt-get install -y -d --no-install-recommends firewalld \
 && useradd -m -s /bin/bash tester \
 && echo 'tester ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/tester \
 && mkdir -p /home/tester/.ssh && chown tester:tester /home/tester/.ssh && chmod 700 /home/tester/.ssh \
 && systemctl mask getty@tty1.service systemd-modules-load.service console-getty.service \
 && (systemctl enable ssh.service 2>/dev/null || systemctl enable ssh.socket) \
 && printf 'LogLevel VERBOSE\n' > /etc/ssh/sshd_config.d/test.conf
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
