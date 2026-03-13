.PHONY: build clean

BINARY_NAME=gateway-agent.exe
BUILD_DIR=bin

build:
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/agent/

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

lint:
	go vet ./...
