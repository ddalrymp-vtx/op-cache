BINARY := op
PREFIX ?= $(HOME)/dev

.PHONY: all build test install uninstall clean fmt vet

all: test build

build:
	go build -o bin/$(BINARY) .

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

# Install the shim as "op" in PREFIX, which must come before the real op on PATH.
install: test
	@mkdir -p $(PREFIX)
	go build -o $(PREFIX)/$(BINARY) .
	@echo "Installed $(PREFIX)/$(BINARY)"
	@command -v op >/dev/null && echo "PATH resolves 'op' to: $$(command -v op)"

uninstall:
	$(RM) $(PREFIX)/$(BINARY)
	@echo "Removed $(PREFIX)/$(BINARY)"

clean:
	$(RM) -r bin
