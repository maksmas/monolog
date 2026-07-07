# Monolog Makefile.
#
# Common local targets (build, test, vet) plus the bot deployment pipeline
# documented in docs/deploy/README.md.
#
# Deployment vars (override at the command line or via a gitignored
# .env.deploy file sourced by your shell):
#
#   EC2_HOST   target host for `deploy-bot` (e.g. ec2-user@1.2.3.4)
#   EC2_USER   ssh user on the host (default: ec2-user)
#   BIN_DIR    install path on the host (default: /opt/monolog-bot)
#   SVC_NAME   systemd unit name on the host (default: monolog-bot)
#
# Example:
#   make deploy-bot EC2_HOST=ec2-user@1.2.3.4

# Version injected into cmd.Version at build time via ldflags. Override on the
# command line (e.g. `make build VERSION=v0.1.0`); defaults to "dev".
VERSION ?= dev
LDFLAGS  = -ldflags "-X github.com/maksmas/monolog/cmd.Version=$(VERSION)"

EC2_USER ?= ec2-user
BIN_DIR  ?= /opt/monolog-bot
SVC_NAME ?= monolog-bot

# Allow `.env.deploy` to populate EC2_HOST etc. without committing the file.
# Make swallows the include error when the file is absent.
-include .env.deploy

.PHONY: build test vet demo build-bot-linux-arm64 deploy-bot

build:
	go build $(LDFLAGS) -o monolog .

test:
	go test ./...

vet:
	go vet ./...

# Render the README demo gif (docs/img/demo.gif) from docs/demo.tape.
# Requires vhs (`brew install vhs`) and `monolog` on PATH — see the header of
# docs/demo.tape (`make build && export PATH=$PWD:$PATH`).
demo:
	vhs docs/demo.tape

# Build the bot binary for the EC2 t4g.nano target (Linux ARM64). Output goes
# to dist/ so `deploy-bot` has a stable path to scp. The dist/ directory is
# gitignored; it is created on demand here.
build-bot-linux-arm64:
	mkdir -p dist
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/monolog-linux-arm64 .

# Push the freshly built bot binary to the EC2 host and restart the systemd
# unit. Depends on build-bot-linux-arm64 so the artifact is always current.
#
# Requires EC2_HOST to be set (via env or .env.deploy). The remote restart
# uses `sudo` so the configured ec2 user must have NOPASSWD sudo for
# `systemctl restart $(SVC_NAME)` (or run the make target as root remotely).
deploy-bot: build-bot-linux-arm64
	@if [ -z "$(EC2_HOST)" ]; then \
		echo "EC2_HOST is required (set in env or .env.deploy)"; exit 1; \
	fi
	scp dist/monolog-linux-arm64 $(EC2_HOST):/tmp/monolog-linux-arm64
	ssh $(EC2_HOST) "sudo install -m 0755 -o $(EC2_USER) -g $(EC2_USER) /tmp/monolog-linux-arm64 $(BIN_DIR)/monolog-linux-arm64 && sudo systemctl restart $(SVC_NAME) && sudo systemctl status --no-pager $(SVC_NAME)"
