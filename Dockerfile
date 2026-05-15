FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=2.0.0" -o /eightly-agent ./cmd/agent

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /eightly-agent /app/eightly-agent
VOLUME /data
EXPOSE 9001
HEALTHCHECK --interval=10s --timeout=5s --retries=3 \
  CMD wget -q --spider https://localhost:9001/ping --no-check-certificate || exit 1
ENTRYPOINT ["/app/eightly-agent"]
