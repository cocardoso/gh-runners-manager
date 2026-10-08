# The ghrm layer (spec §8.2), built on top of the official ubuntu-slim image, which is
# built unmodified from actions/runner-images. It adds only what an LXC job environment needs.
ARG BASE=ghrm-slim
FROM ${BASE}

ARG RUNNER_VERSION
ARG RUNNER_SHA256
ARG LAYER_VERSION
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive

COPY apt-ipv4.conf /etc/apt/apt.conf.d/99ghrm-ipv4

# docker export drops the image's ENV: keep it in /etc/environment, where jobs read it
# (the image's values win over placeholders its scripts wrote there), with LANG=C.UTF-8.
COPY persist-env.sh /tmp/persist-env.sh
RUN /tmp/persist-env.sh /etc/environment IMAGE_OWNER ImageVersion IMAGE_VERSION ImageOS IMAGE_TARGET_PLATFORM \
      POWERSHELL_DISTRIBUTION_CHANNEL IMAGEDATA_NAME NVM_DIR \
 && rm /tmp/persist-env.sh \
 && echo 'LANG=C.UTF-8' > /etc/default/locale

# systemd as init and the Docker Engine (the slim image ships only the Docker CLI and plugins).
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv dbus iproute2 iputils-ping \
      docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin \
 && systemctl enable systemd-networkd.service docker.service containerd.service

# The runner user, as on hosted runners: passwordless sudo and the docker group.
RUN (id runner >/dev/null 2>&1 || useradd -m -s /bin/bash -u 1001 runner) \
 && usermod -aG docker runner \
 && echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner \
 && chmod 0440 /etc/sudoers.d/runner

# The GitHub Actions runner, verified against the checksum published with the release.
RUN mkdir -p /home/runner/actions-runner \
 && cd /home/runner/actions-runner \
 && curl -fsSL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz" \
 && echo "${RUNNER_SHA256}  runner.tar.gz" | sha256sum -c - \
 && tar xzf runner.tar.gz \
 && rm runner.tar.gz \
 && ./bin/installdependencies.sh \
 && chown -R runner:runner /home/runner

# Pull images through the registry cache on the job network, when one is configured (M6).
ARG CACHE_MIRRORS=""
COPY mirrors.sh /tmp/mirrors.sh
RUN /tmp/mirrors.sh / "$CACHE_MIRRORS" && rm /tmp/mirrors.sh

COPY ghrm-agent /usr/local/bin/ghrm-agent
COPY ghrm-agent.service /etc/systemd/system/ghrm-agent.service

# Every clone must get its own identity; the slim build marks itself as a container.
RUN systemctl enable ghrm-agent.service \
 && rm -f /run/.containerenv \
 && truncate -s 0 /etc/machine-id \
 && rm -f /var/lib/dbus/machine-id /etc/ssh/ssh_host_* \
 && echo "${LAYER_VERSION}" > /etc/ghrm-layer-version \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/* /tmp/*

STOPSIGNAL SIGRTMIN+3
