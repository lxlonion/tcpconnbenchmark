BINARY ?= tcpconnbenchmark
APP    := tcpconnbenchmark.app
GOFLAGS ?=
LDFLAGS ?= -s -w
HOST_GOOS = $(shell GOFLAGS= go env GOHOSTOS)
HOST_GOARCH = $(shell GOFLAGS= go env GOHOSTARCH)

export GOFLAGS
export TCPCONNBENCHMARK_BUILD_BINARY := $(BINARY)
export TCPCONNBENCHMARK_BUILD_APP := $(APP)
export TCPCONNBENCHMARK_BUILD_LDFLAGS := $(LDFLAGS)
export TCPCONNBENCHMARK_TARGET_GOOS = $(if $(GOOS),$(GOOS),$(shell GOFLAGS= go env GOOS))
export TCPCONNBENCHMARK_TARGET_GOARCH = $(if $(GOARCH),$(GOARCH),$(shell GOFLAGS= go env GOARCH))

.PHONY: build app run test vet clean

build:
	GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) GOFLAGS= CGO_ENABLED=0 go run -buildvcs=false ./scripts/buildmeta build $(GOFLAGS)

app:
	GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) GOFLAGS= CGO_ENABLED=0 go run -buildvcs=false ./scripts/buildmeta app $(GOFLAGS)

run:
	GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) GOFLAGS= CGO_ENABLED=0 go run -buildvcs=false ./scripts/buildmeta run $(GOFLAGS)

test:
	CGO_ENABLED=0 go test ./...

vet:
	CGO_ENABLED=0 go vet ./...

clean:
	rm -rf $(BINARY) $(APP)
