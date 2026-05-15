VERSION ?= 2.0.0
BINARY = eightly-agent
LDFLAGS = -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build test docker clean

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o build/$(BINARY) ./cmd/agent

test:
	go test ./...

docker:
	docker build -t ghcr.io/smashingtags/eightly-agent:$(VERSION) .

push: docker
	docker push ghcr.io/smashingtags/eightly-agent:$(VERSION)

clean:
	rm -rf build/
