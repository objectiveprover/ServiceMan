# ServiceMan follows the service-project convention: `build` makes one
# executable, `install` puts it in ~/.local/bin and the config template in
# ~/.config/serviceman/, `uninstall` removes the executable, `clean` the build.

BIN        := serviceman
PREFIX     ?= $(HOME)/.local
BINDIR     := $(PREFIX)/bin
CONFIG_DIR := $(HOME)/.config/$(BIN)
CONFIG     := $(CONFIG_DIR)/config.json
TEMPLATE   := config.template.json

build:
	go build -o $(BIN) .

# An existing config is never changed without asking: Keep (default),
# Update (add the template's missing fields; nothing is removed or changed),
# or Replace (after a timestamped backup). Without a terminal it is kept.
install: build
	install -Dm755 $(BIN) $(BINDIR)/$(BIN)
	@echo "installed $(BINDIR)/$(BIN)"
	@set -e; \
	if [ ! -e "$(CONFIG)" ]; then \
		install -Dm644 $(TEMPLATE) "$(CONFIG)"; \
		echo "installed $(CONFIG) from $(TEMPLATE)"; \
	elif [ ! -t 0 ]; then \
		echo "kept the existing $(CONFIG) (no terminal to ask; run make install from a terminal to update or replace it)"; \
	else \
		printf '%s already exists. [K]eep it, [u]pdate it with new fields, or [r]eplace it (a backup is kept)? ' "$(CONFIG)"; \
		read answer; \
		case "$$answer" in \
		u|U) $(BINDIR)/$(BIN) config update "$(CONFIG)" ;; \
		r|R) backup="$(CONFIG).$$(date +%Y%m%d-%H%M%S).bak"; \
		     cp -p "$(CONFIG)" "$$backup"; \
		     install -m644 $(TEMPLATE) "$(CONFIG)"; \
		     echo "replaced $(CONFIG) (backup: $$backup)" ;; \
		*)   echo "kept $(CONFIG)" ;; \
		esac; \
	fi
	@echo "start the ServiceMan daemon with 'serviceman daemon install' (or D in the TUI) if it isn't running"

# Stops the ServiceMan daemon first: its unit runs this executable and would
# otherwise keep failing. The config and log files are left alone.
uninstall:
	@if [ -x $(BINDIR)/$(BIN) ]; then $(BINDIR)/$(BIN) daemon uninstall; fi
	rm -f $(BINDIR)/$(BIN)
	@echo "removed $(BINDIR)/$(BIN); config kept in $(CONFIG_DIR)"

clean:
	rm -f $(BIN)

vet:
	go vet ./...

.PHONY: build install uninstall clean vet
