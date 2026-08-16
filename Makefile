VERSION ?= 1.0.0
BINARY = eightly-agent
LDFLAGS = -ldflags "-s -w -X main.version=$(VERSION)"
GHCR_IMAGE = ghcr.io/imogenlabs/eightly-agent
DOCKERHUB_IMAGE = smashingtags/eightly-agent

.PHONY: build test lint docker push clean

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o build/$(BINARY) ./cmd/agent

test:
	go test ./... -count=1 -race -timeout 60s

lint:
	go vet ./...

docker:
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		-t $(GHCR_IMAGE):$(VERSION) \
		-t $(GHCR_IMAGE):latest \
		-t $(DOCKERHUB_IMAGE):$(VERSION) \
		-t $(DOCKERHUB_IMAGE):latest \
		--push .

docker-local:
	docker build --build-arg VERSION=$(VERSION) \
		-t $(GHCR_IMAGE):$(VERSION) .

push-ghcr:
	docker push $(GHCR_IMAGE):$(VERSION)
	docker push $(GHCR_IMAGE):latest

push-dockerhub:
	docker push $(DOCKERHUB_IMAGE):$(VERSION)
	docker push $(DOCKERHUB_IMAGE):latest

clean:
	rm -rf build/
