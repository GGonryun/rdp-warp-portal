.PHONY: build clean test lint

BUILD_DIR=bin

build:
	@echo "=== Building gateway-agent.exe ==="
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/gateway-agent.exe ./cmd/gateway-agent/
	@echo ""
	@echo "=== Build complete ==="
	@echo "Transfer $(BUILD_DIR)/gateway-agent.exe to the bastion and run:"
	@echo "  gateway-agent.exe --install"

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

lint:
	go vet ./...
