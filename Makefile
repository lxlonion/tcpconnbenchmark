BINARY ?= TCPLink
APP    := TCPLink.app
GOFLAGS ?=
LDFLAGS := -s -w

.PHONY: build app run test vet clean

build:
	CGO_ENABLED=1 go build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

app: build
	@if [ "$$(uname -s)" != "Darwin" ]; then echo "make app 仅支持 macOS"; exit 1; fi
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp packaging/Info.plist $(APP)/Contents/Info.plist
	cp $(BINARY) $(APP)/Contents/MacOS/TCPLink
	codesign --force --deep --sign - $(APP) >/dev/null 2>&1 || true
	@echo "已生成 $(APP)，运行: open $(APP)"

run:
	go run .

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(BINARY) $(APP)
