FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN apk add --no-cache git ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o /eightly-agent ./cmd/agent

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /eightly-agent /app/eightly-agent
VOLUME /data
EXPOSE 9001
HEALTHCHECK --interval=10s --timeout=5s --retries=3 \
  CMD wget -q --spider https://localhost:9001/ping --no-check-certificate || exit 1
ENTRYPOINT ["/app/eightly-agent"]

LABEL org.opencontainers.image.title="Eight.ly Agent"
LABEL org.opencontainers.image.description="Lightweight Docker management agent for Eight.ly OS"
LABEL org.opencontainers.image.vendor="Imogen Labs"
LABEL org.opencontainers.image.source="https://github.com/smashingtags/eightly-agent"
