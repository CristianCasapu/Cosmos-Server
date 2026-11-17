# A test machine for scripts/fork-guard-e2e.sh: systemd, sshd, Docker, and the
# packages the protection pieces install, downloaded but not installed (the test
# networks have no way out: apt-get installs them from the cache).
ARG BASE=debian:13
FROM ${BASE}
ENV DEBIAN_FRONTEND=noninteractive
RUN rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv dbus openssh-server \
      iproute2 busybox curl ca-certificates netcat-openbsd procps docker.io kmod sudo \
      conntrack util-linux \
 && (apt-get install -y --no-install-recommends docker-cli || true) \
 && apt-get install -y -d --no-install-recommends nftables fail2ban python3 unattended-upgrades \
 && apt-get install -y -d --no-install-recommends ufw \
 && apt-get install -y -d --no-install-recommends firewalld \
 && useradd -m -s /bin/bash tester \
 && echo 'tester ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/tester \
 && mkdir -p /home/tester/.ssh && chown tester:tester /home/tester/.ssh && chmod 700 /home/tester/.ssh \
 && systemctl mask getty@tty1.service systemd-modules-load.service console-getty.service \
 && (systemctl enable ssh.service 2>/dev/null || systemctl enable ssh.socket) \
 && printf 'LogLevel VERBOSE\n' > /etc/ssh/sshd_config.d/test.conf
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
