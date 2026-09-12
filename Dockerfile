# syntax=docker/dockerfile:1
# Agent-only image: compile no server binary or web assets.
FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY cmd/agent ./cmd/agent
COPY bootstrap ./bootstrap
COPY executor ./executor
COPY internal ./internal
COPY payload ./payload
COPY protocol ./protocol
COPY state ./state
COPY transport ./transport
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -trimpath \
    -o /out/durpdeploy-agent ./cmd/agent

FROM alpine:3.20

# hadolint ignore=DL3018
RUN apk add --no-cache bash ca-certificates && \
	adduser -D -H -s /sbin/nologin -u 10001 durpdeploy-agent && \
	mkdir -p /var/lib/durpdeploy-agent /tmp && \
	chown durpdeploy-agent:durpdeploy-agent /var/lib/durpdeploy-agent /tmp && \
	chmod 0700 /var/lib/durpdeploy-agent

ENV DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent
ENV DURPDEPLOY_AGENT_EXECUTION_BOUNDARY=service
ENV TMPDIR=/tmp
WORKDIR /var/lib/durpdeploy-agent
VOLUME ["/var/lib/durpdeploy-agent", "/tmp"]

COPY --from=builder /out/durpdeploy-agent /usr/local/bin/durpdeploy-agent
RUN chmod 0755 /usr/local/bin/durpdeploy-agent

USER 10001
ENTRYPOINT ["/usr/local/bin/durpdeploy-agent"]
