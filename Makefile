.PHONY: build run clean

build:
	@go build -o bin/nevinho

run:
	./bin/nevinho

clean:
	@rm -rf bin
