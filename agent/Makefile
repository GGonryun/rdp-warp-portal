.PHONY: build clean

build:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o agent.exe ./cmd/p0rtal-agent

clean:
	rm -f agent.exe
