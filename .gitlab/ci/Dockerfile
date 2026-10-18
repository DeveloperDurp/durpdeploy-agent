# syntax=harbor.durp.info/dockerhub/docker/dockerfile:1
# Tests need real local Unix sockets, bind mounts, and a rootless user manager.
# This image runs as a disposable systemd container inside each job's DinD service.
FROM harbor.durp.info/dockerhub/library/golang:1.26.9-bookworm AS go
WORKDIR /ci-tools-src
COPY .gitlab/ci/go.mod .gitlab/ci/go.sum ./
RUN CGO_ENABLED=0 GOBIN=/ci-tools go install -mod=readonly \
      gotest.tools/gotestsum github.com/boumenot/gocover-cobertura
FROM harbor.durp.info/dockerhub/library/docker:29-dind AS docker
FROM harbor.durp.info/dockerhub/library/fedora:44

RUN dnf install -y --setopt=install_weak_deps=False \
      bash make gcc git-core python3-pyyaml python3-coverage python3-pytest podman skopeo slirp4netns \
      shadow-utils libcap systemd systemd-pam dbus-daemon dbus-broker nodejs npm iptables-nft iproute \
      procps-ng util-linux findutils diffutils tar gzip which && dnf clean all
COPY --from=go /usr/local/go /usr/local/go
COPY --from=go /ci-tools/ /usr/local/bin/
COPY --from=docker /usr/local/bin/ /usr/local/bin/
COPY --from=docker /usr/local/libexec/docker/cli-plugins/ /usr/local/libexec/docker/cli-plugins/
ENV PATH=/usr/local/go/bin:$PATH
ENV container=docker

# The rootless mapping helpers need only their respective ID-mapping capability.
RUN chmod 0755 /usr/sbin/newuidmap /usr/sbin/newgidmap && \
    setcap cap_setuid=ep /usr/sbin/newuidmap && \
    setcap cap_setgid=ep /usr/sbin/newgidmap && \
    useradd --create-home --uid 1000 ci && \
    printf 'ci:100000:65536\n' > /etc/subuid && \
    printf 'ci:100000:65536\n' > /etc/subgid && \
    mkdir -p /etc/systemd/system/user@.service.d /etc/containers \
      /var/lib/systemd/linger && \
    touch /var/lib/systemd/linger/ci && \
    printf '[Service]\nDelegate=cpu cpuset io memory pids\n' \
      > /etc/systemd/system/user@.service.d/delegate.conf && \
    printf '[storage]\ndriver="vfs"\n' > /etc/containers/storage.conf && \
    printf '[engine]\ncgroup_manager="systemd"\nevents_logger="file"\n' \
      > /etc/containers/containers.conf && \
    mkdir -p /etc/containers/registries.conf.d && \
    printf '[[registry]]\nprefix="docker.io"\nlocation="harbor.durp.info/dockerhub"\n' \
      > /etc/containers/registries.conf.d/harbor.conf
WORKDIR /workspace
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
