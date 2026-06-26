.PHONY: build test clean install uninstall remove configure

BINARY_NAME=hyperhive
BUILD_DIR=.
CMD_PATH=./cmd/hyperhive
INSTALL_PREFIX=/usr/local
SYSTEMD_DIR=/etc/systemd/system
SERVICE_CONFIG=/etc/hyperhive/config.json
LOGIN_INTERVAL=10
MOUNT_INTERVAL=10
SERVICE_UNIT = [Unit]\nDescription=HyperHive NFS mount service\nAfter=network-online.target\nWants=network-online.target\nStartLimitIntervalSec=0\n\n[Service]\nType=simple\nUser=root\nEnvironment=HYPERHIVE_CONFIG=$(SERVICE_CONFIG)\nEnvironment=HYPERHIVE_LOGIN_INTERVAL=$(LOGIN_INTERVAL)\nEnvironment=HYPERHIVE_MOUNT_INTERVAL=$(MOUNT_INTERVAL)\nExecStart=$(INSTALL_PREFIX)/bin/$(BINARY_NAME) systemdexec\nRestart=always\nRestartSec=10\nTimeoutStopSec=30\n\n[Install]\nWantedBy=multi-user.target

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

test:
	go test ./...

clean:
	rm -f $(BUILD_DIR)/$(BINARY_NAME)

configure:
	@echo "Configuring HyperHive service credentials..."
	sudo env HYPERHIVE_CONFIG=$(SERVICE_CONFIG) $(INSTALL_PREFIX)/bin/$(BINARY_NAME) setup
	sudo env HYPERHIVE_CONFIG=$(SERVICE_CONFIG) $(INSTALL_PREFIX)/bin/$(BINARY_NAME) login

install: build
	sudo install -d $(INSTALL_PREFIX)/bin
	sudo install -m 0755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_PREFIX)/bin/$(BINARY_NAME)
	@printf '$(SERVICE_UNIT)\n' | sudo tee $(SYSTEMD_DIR)/hyperhive.service > /dev/null
	sudo systemctl daemon-reload
	sudo systemctl enable hyperhive.service
	@if sudo test ! -f $(SERVICE_CONFIG); then \
		$(MAKE) configure; \
	else \
		echo "Config already exists at $(SERVICE_CONFIG) (skipping setup/login)"; \
		echo "To reconfigure run: make configure"; \
	fi
	sudo systemctl restart hyperhive.service
	@echo "Service installed. Check status with: systemctl status hyperhive"
	@echo "View logs with: hyperhive logs  or  journalctl -u hyperhive"
	@echo "Intervals: login=$(LOGIN_INTERVAL)m mount=$(MOUNT_INTERVAL)m (override with: make install LOGIN_INTERVAL=5 MOUNT_INTERVAL=5)"

uninstall remove:
	sudo systemctl stop hyperhive.service || true
	@if sudo test -f $(SERVICE_CONFIG); then \
		sudo env HYPERHIVE_CONFIG=$(SERVICE_CONFIG) $(INSTALL_PREFIX)/bin/$(BINARY_NAME) remove_nfs || true; \
	fi
	sudo systemctl disable hyperhive.service || true
	sudo rm -f $(SYSTEMD_DIR)/hyperhive.service
	sudo systemctl daemon-reload
	sudo rm -f $(INSTALL_PREFIX)/bin/$(BINARY_NAME)
	sudo rm -rf /etc/hyperhive
	@echo "HyperHive removed."
