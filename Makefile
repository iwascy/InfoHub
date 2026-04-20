APP := infohub

.PHONY: build run fmt test tidy

build:
	go build -o bin/$(APP) ./cmd/infohub

run:
	go run ./cmd/infohub -config config.yaml

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

tidy:
	go mod tidy
