.PHONY: build build-wayland install install-wayland uninstall clean test fmt lint check bump

SYSTEMD_USER_DIR := $(HOME)/.config/systemd/user

build:
	go build -o mwb ./cmd/mwb

# build-wayland adds the InputCapture-portal edge-detection backend for
# native Wayland compositors (KWin 6.1+, GNOME 46+, wlroots + EIS). Requires
# libei-1.0 development headers (pkg-config libei-1.0) and a C toolchain;
# not part of the default build so release artifacts stay CGO_ENABLED=0.
build-wayland:
	CGO_ENABLED=1 go build -tags wayland_portal -o mwb ./cmd/mwb

install: build
	install -D mwb $(HOME)/go/bin/mwb
	install -d $(SYSTEMD_USER_DIR)
	install -m 644 mwb.service $(SYSTEMD_USER_DIR)/mwb.service
	systemctl --user daemon-reload
	@echo ""
	@echo "Installed. To start:"
	@echo "  systemctl --user enable --now mwb"
	@echo ""
	@echo "View logs:"
	@echo "  journalctl --user -u mwb -f"

# Same as install, but with the Wayland InputCapture portal backend built
# in (see build-wayland). Set capture_backend = "portal" (or "auto") in
# config.toml to use it.
install-wayland: build-wayland
	install -D mwb $(HOME)/go/bin/mwb
	install -d $(SYSTEMD_USER_DIR)
	install -m 644 mwb.service $(SYSTEMD_USER_DIR)/mwb.service
	systemctl --user daemon-reload
	@echo ""
	@echo "Installed with Wayland portal support. To start:"
	@echo "  systemctl --user enable --now mwb"
	@echo ""
	@echo "View logs:"
	@echo "  journalctl --user -u mwb -f"

uninstall:
	systemctl --user disable --now mwb || true
	rm -f $(SYSTEMD_USER_DIR)/mwb.service
	rm -f $(HOME)/go/bin/mwb
	systemctl --user daemon-reload

clean:
	rm -f mwb

test:
	go test ./...

fmt:
	gofmt -w .

lint:
	golangci-lint run

check: fmt lint test

bump: ## generate a new version with svu
	@$(MAKE) build
	@$(MAKE) test
	@$(MAKE) fmt
	$(MAKE) lint
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Working directory is not clean. Please commit or stash changes before bumping version."; \
		exit 1; \
	fi
	@echo "Creating new tag..."
	@version=$$(svu next); \
		git tag -a $$version -m "Version $$version"; \
		echo "Tagged version $$version"; \
		echo "Pushing tag $$version to origin..."; \
		git push origin $$version

