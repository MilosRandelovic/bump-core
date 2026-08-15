BINARY_NAME=bump-core

.PHONY: build test clean

build:
	go build -o $(BINARY_NAME) ./cmd/bump-core

test:
	go test ./...

clean:
	rm -f $(BINARY_NAME)
