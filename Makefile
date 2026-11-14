PREFIX ?= $(HOME)/.local

build:
	go build -o svcman .

install: build
	install -Dm755 svcman $(PREFIX)/bin/svcman
	@echo "installed $(PREFIX)/bin/svcman — run 'svcman logger install' to start log recording"

vet:
	go vet ./...

.PHONY: build install vet
