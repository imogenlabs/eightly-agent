# Base images are pinned by digest (supply-chain integrity); the tag is kept as a
# human-readable comment. Dependabot monitors for updates via .github/dependabot.yml.
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS builder
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
RUN apk add --no-cache git ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN export GOARM="${TARGETVARIANT#v}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${GOARM} \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o /eightly-agent ./cmd/agent

FROM alpine:3.23@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11
RUN apk add --no-cache ca-certificates tzdata tini && \
    if ! awk -F: '$3==999' /etc/group | grep -q .; then addgroup -g 999 docker; fi && \
    DOCKER_GRP="$(awk -F: '$3==999{print $1; exit}' /etc/group)" && \
    adduser -S -u 1000 -G "$DOCKER_GRP" eightly-agent && \
    mkdir -p /data && chown "eightly-agent:$DOCKER_GRP" /data
COPY --from=builder /eightly-agent /app/eightly-agent
VOLUME /data
EXPOSE 9001
USER eightly-agent
# The check runs in-process: busybox wget forked an ssl_client
# helper, and every check Docker timed out left that helper behind as a zombie
# (115,420 on a production host). tini is PID 1 so anything orphaned is still reaped.
# -s registers it as a subreaper, so under `docker run --init` (where Docker's
# own init is PID 1) it still reaps and does not warn on every start.
HEALTHCHECK --interval=10s --timeout=5s --retries=3 \
  CMD ["/app/eightly-agent", "healthcheck"]
ENTRYPOINT ["/sbin/tini", "-s", "--", "/app/eightly-agent"]

LABEL org.opencontainers.image.title="Eight.ly Agent"
LABEL org.opencontainers.image.description="Lightweight Docker management agent for Eight.ly OS"
LABEL org.opencontainers.image.vendor="Imogen Labs"
LABEL org.opencontainers.image.source="https://github.com/imogenlabs/eightly-agent"
