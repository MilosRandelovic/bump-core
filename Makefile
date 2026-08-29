CORE_BINARY_NAME=bump-core
MCP_BINARY_NAME=bump-mcp

.PHONY: build test clean

build:
	go build -o $(CORE_BINARY_NAME) ./cmd/bump-core
	go build -o $(MCP_BINARY_NAME) ./cmd/bump-mcp

test:
	go test ./...

clean:
	rm -f $(CORE_BINARY_NAME) $(MCP_BINARY_NAME)
