# syntax=harbor.durp.info/dockerhub/docker/dockerfile:1
# Agent-only image: compile no server binary or web assets.
FROM --platform=$BUILDPLATFORM harbor.durp.info/dockerhub/library/golang:1.26-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache bash

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/agent ./cmd/agent
COPY artifact ./artifact
COPY bootstrap ./bootstrap
COPY configuration ./configuration
COPY executor ./executor
COPY internal ./internal
COPY payload ./payload
COPY protocol ./protocol
COPY resourcepolicy ./resourcepolicy
COPY state ./state
COPY transport ./transport
COPY scripts/build_metadata.sh scripts/build_agent.sh ./scripts/
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
ARG BUILD_NUMBER=unknown
ARG BUILD_MODIFIED=false
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH bash scripts/build_agent.sh -trimpath \
    -o /out/durpdeploy-agent

FROM harbor.durp.info/dockerhub/library/alpine:3.24

COPY third_party/go-yaml /usr/share/licenses/durpdeploy-agent/go-yaml/

ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
ARG BUILD_NUMBER=unknown
LABEL org.opencontainers.image.version=$BUILD_VERSION \
      org.opencontainers.image.revision=$BUILD_COMMIT \
      info.durpdeploy.build-number=$BUILD_NUMBER

# hadolint ignore=DL3018
RUN apk upgrade --no-cache && \
	apk add --no-cache bash ca-certificates docker-cli podman-remote && \
	ln -s /usr/bin/podman-remote /usr/local/bin/podman && \
	adduser -D -H -s /sbin/nologin -u 10001 durpdeploy-agent && \
	mkdir -p /var/lib/durpdeploy-agent /tmp && \
	chown durpdeploy-agent:durpdeploy-agent /var/lib/durpdeploy-agent /tmp && \
	chmod 0700 /var/lib/durpdeploy-agent

ENV DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent
ENV DURPDEPLOY_AGENT_EXECUTION_BOUNDARY=service
ENV container=durpdeploy
ENV TMPDIR=/tmp
WORKDIR /var/lib/durpdeploy-agent
VOLUME ["/var/lib/durpdeploy-agent", "/tmp"]

COPY --from=builder /out/durpdeploy-agent /usr/local/bin/durpdeploy-agent
RUN chmod 0755 /usr/local/bin/durpdeploy-agent

USER 10001
ENTRYPOINT ["/usr/local/bin/durpdeploy-agent"]
