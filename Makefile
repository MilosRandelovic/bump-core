CORE_BINARY_NAME := bump-core
MCP_BINARY_NAME := bump-mcp
ACTIONLINT_VERSION := v1.7.12

.PHONY: all build test clean workflow-lint

build:
	go build -o $(CORE_BINARY_NAME) ./cmd/bump-core
	go build -o $(MCP_BINARY_NAME) ./cmd/bump-mcp

test:
	go test ./...

clean:
	rm -f $(CORE_BINARY_NAME) $(MCP_BINARY_NAME)

workflow-lint:
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml
