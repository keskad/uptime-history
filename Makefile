.PHONY: all
all: build

build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -o bin/uptime-history main.go
	chmod +x bin/uptime-history
