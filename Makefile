.PHONY: build test fmt vet

build:
	go build -o bin/spotify-to-musicdl ./cmd/spotify-to-musicdl

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet:
	go vet ./...
