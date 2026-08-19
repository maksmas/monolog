# Monolog Makefile.
#
# Common local targets (build, test, vet) plus the bot deployment pipeline
# documented in docs/deploy/README.md.
#
# Deployment vars (override at the command line or via a gitignored
# .env.deploy file sourced by your shell):
#
#   DEPLOY_HOST  ssh target for `deploy-bot` (e.g. you@monolog-bot.local).
#                This must be a LOGIN user with sudo -- not the monolog-bot
#                service user, which is created with /usr/sbin/nologin.
#   BOT_ARCH     GOARCH of the host: arm64 (Pi, Graviton) or amd64 (x86 box)
#   BIN_DIR      install path on the host (default: /opt/monolog-bot)
#   SVC_NAME     systemd unit name on the host (default: monolog-bot)
#
# EC2_HOST / EC2_USER remain accepted as aliases so existing .env.deploy
# files keep working; DEPLOY_* wins when both are set.
#
# Example:
#   make deploy-bot DEPLOY_HOST=monolog-bot@monolog-bot.local BOT_ARCH=amd64

# Version injected into cmd.Version at build time via ldflags. Override on the
# command line (e.g. `make build VERSION=v0.1.0`); defaults to "dev".
VERSION ?= dev
LDFLAGS  = -ldflags "-X github.com/maksmas/monolog/cmd.Version=$(VERSION)"

# GOARCH of the target host. arm64 for a Raspberry Pi or an ARM VPS; amd64
# for an x86 box (old laptop, mini PC, most budget VPS tiers).
BOT_ARCH ?= arm64
BOT_BIN   = monolog-linux-$(BOT_ARCH)

DEPLOY_HOST ?= $(EC2_HOST)
BIN_DIR     ?= /opt/monolog-bot
SVC_NAME    ?= monolog-bot

# Allow `.env.deploy` to populate EC2_HOST etc. without committing the file.
# Make swallows the include error when the file is absent.
-include .env.deploy

.PHONY: build test vet demo build-bot-linux build-bot-linux-arm64 build-bot-linux-amd64 deploy-bot

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

# Cross-compile the bot binary for the target host. Output goes to dist/ so
# `deploy-bot` has a stable path to scp. The dist/ directory is gitignored;
# it is created on demand here.
build-bot-linux:
	mkdir -p dist
	GOOS=linux GOARCH=$(BOT_ARCH) go build $(LDFLAGS) -o dist/$(BOT_BIN) .

# Convenience aliases so the arch is visible in the command you type.
build-bot-linux-arm64:
	$(MAKE) build-bot-linux BOT_ARCH=arm64

build-bot-linux-amd64:
	$(MAKE) build-bot-linux BOT_ARCH=amd64

# Push the freshly built bot binary to the EC2 host and restart the systemd
# unit. Depends on build-bot-linux-arm64 so the artifact is always current.
#
# Requires DEPLOY_HOST to be set (via env or .env.deploy). The remote restart
# uses `sudo` so the deploy user must have NOPASSWD sudo for
# `systemctl restart $(SVC_NAME)` (or run the make target as root remotely).
#
# The binary is installed as $(BIN_DIR)/monolog regardless of arch, so the
# systemd unit's ExecStart never has to know what it was built for. It is
# owned root:root mode 0755: the service user needs only read+execute, and a
# compromised bot must not be able to rewrite its own binary.
deploy-bot: build-bot-linux
	@if [ -z "$(DEPLOY_HOST)" ]; then \
		echo "DEPLOY_HOST is required (set in env or .env.deploy)"; exit 1; \
	fi
	scp dist/$(BOT_BIN) $(DEPLOY_HOST):/tmp/$(BOT_BIN)
	ssh $(DEPLOY_HOST) "sudo install -m 0755 -o root -g root /tmp/$(BOT_BIN) $(BIN_DIR)/monolog && rm -f /tmp/$(BOT_BIN) && sudo systemctl restart $(SVC_NAME) && sudo systemctl status --no-pager $(SVC_NAME)"
