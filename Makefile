# make install puts the claudewitz command in Go's bin folder ($GOBIN, else
# $GOPATH/bin, usually ~/go/bin) and says how to add it to PATH if needed.

BIN := claudewitz
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build install uninstall test

build:
	go build -o $(BIN) .

install:
	GOBIN=$(GOBIN) go install .
	@case ":$$PATH:" in \
	  *":$(GOBIN):"*) echo "Installed $(GOBIN)/$(BIN). Run claudewitz from any directory." ;; \
	  *) echo "Installed $(GOBIN)/$(BIN), but $(GOBIN) is not on your PATH."; \
	     echo "Add this line to your shell profile (~/.zshrc, ~/.bashrc), then open a new terminal:"; \
	     echo "  export PATH=\"$(GOBIN):\$$PATH\"" ;; \
	esac

uninstall:
	rm -f $(GOBIN)/$(BIN)

test:
	go vet ./...
	go test ./...
