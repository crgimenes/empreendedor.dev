BINARY_NAME=edev

export CGO_ENABLED=0
GIT_TAG := $(shell git describe --tags --always)
BUILD_FLAGS := -trimpath -ldflags "-X 'main.GitTag=$(GIT_TAG)' -s -w -extldflags '-static -w'"

.PHONY: all build build-cross clean

all: build

build:
	# Build for the current OS and architecture
	go build $(BUILD_FLAGS) -o $(BINARY_NAME)-$(shell go env GOOS)-$(shell go env GOARCH) .
	#Linux amd64 build
	GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o $(BINARY_NAME)-linux-amd64 .
	#FreeBSD amd64 build
	GOOS=freebsd GOARCH=amd64 go build $(BUILD_FLAGS) -o $(BINARY_NAME)-freebsd-amd64 .


dev:
	go run -tags dev -trimpath .

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-*

clean-all: clean
	rm -f *.db-shm *.db-wal *.db sessions.gob *.log filo-cli
	rm -rf dist data



