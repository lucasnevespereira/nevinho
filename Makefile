.PHONY: build run clean lint test

lint:
	@golangci-lint run ./...

build:
	@go build -ldflags "-X main.version=$$(git describe --tags --always)" -o bin/nevinho

run:
	@go run main.go

test:
	@go test ./... -count=1

clean:
	@rm -rf bin
