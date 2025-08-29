BINARY_NAME=$(shell basename $(shell pwd))

export CGO_ENABLED=0
GIT_TAG := $(shell git describe --tags --always)
BUILD_FLAGS := -trimpath -ldflags "-X 'main.GitTag=$(GIT_TAG)' -s -w -extldflags '-static -w'"

.PHONY: all build build-cross clean

all: build

build:
	# Build for the current OS and architecture
	go build $(BUILD_FLAGS) -o $(BINARY_NAME) .
	#Linux amd64 build
	GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o $(BINARY_NAME)-linux-amd64 .

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-*

