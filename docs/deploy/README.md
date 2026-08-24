# Monolog Telegram Bot — Deployment

Setup for running `monolog telegram serve` on an always-on host. The bot is
just another monolog client: it owns a clone of the tasks git repo and uses
the same `store` + `git` paths the CLI uses. Once running, the laptop side
gets no new wiring — every laptop mutation already auto-pushes to the
remote, and `s` sync plus the fsnotify watcher handle the read side.

**You never have to sync by hand for the bot to see a change.** A task you
add on the laptop is pushed in the background, and the bot pulls before it
serves any command that reads or acts on existing state — so `/today` on
your phone reflects the laptop within seconds, not on the next tick.

**The bot needs no inbound connectivity.** It long-polls Telegram outbound
and pushes to GitHub outbound — no webhook, no public IP, no port forward.
A machine behind home NAT is a perfectly valid host, which is why the
self-hosted path below is the default rather than a cloud VM.

Each numbered step is meant to be ticked off as you go. If something breaks,
jump to [Troubleshooting](#troubleshooting) — it is indexed by the exact
error text you will see.

## 0. Prerequisite: a shared git remote

The whole design routes through a remote both machines can reach. Check:

```sh
git -C "${MONOLOG_DIR:-$HOME/.monolog}" remote -v
```

- [ ] If that prints nothing, create one before going further. A private
      GitHub repo is the usual choice:
      `gh repo create <you>/monolog-tasks --private --source="${MONOLOG_DIR:-$HOME/.monolog}" --push`
- [ ] Note that this puts task titles and notes on the remote's servers.
      Private, but no longer only on your disk.
- [ ] Do **not** host the remote on the bot machine itself — your laptop
      would then only sync when at home, which defeats capturing from your
      phone while out.

## 1. Choose and prepare a host

Any always-on Linux box with systemd. Ranked by how little work they are:

| Host | Notes |
|---|---|
| **Old laptop / mini PC / Pi you own** | Free. A laptop has a built-in UPS (its battery). `BOT_ARCH=amd64` for x86, `arm64` for a Pi. |
| **Small VPS** (Hetzner CAX11, DO, Vultr) | ~€4/mo. Pick the arch to match `BOT_ARCH`. |
| **Cloud VM** (e.g. EC2 t4g.nano) | ARM64 on Graviton, `BOT_ARCH=arm64`. Lock the firewall down: inbound SSH from your IP only; outbound HTTPS open. |

Whatever you pick:

- [ ] Install a **server** OS with no desktop. Debian stable netinst is a
      good default — at the tasksel screen uncheck the desktop
      environments and **check "SSH server"**. Forgetting SSH server is the
      most common way to end up with a host you cannot deploy to.
- [ ] Confirm the arch: `uname -m` → `x86_64` means `BOT_ARCH=amd64`,
      `aarch64` means `BOT_ARCH=arm64`.
- [ ] Install git: `sudo apt install -y git`

### 1a. If the host is a laptop

Laptops suspend themselves; servers must not.

```sh
sudo mkdir -p /etc/systemd/logind.conf.d
sudo tee /etc/systemd/logind.conf.d/99-server.conf >/dev/null <<'EOF'
[Login]
HandleLidSwitch=ignore
HandleLidSwitchDocked=ignore
HandleLidSwitchExternalPower=ignore
EOF
sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target
```

- [ ] Reboot to apply (restarting `systemd-logind` in place can drop your
      SSH session)
- [ ] If the battery is swollen, remove it and run on AC — this machine
      will sit unattended
- [ ] Prefer Ethernet. Wi-Fi works, but must come up at boot with nobody
      logged in

### 1b. A stable address to deploy to

```sh
sudo hostnamectl set-hostname monolog-bot
sudo apt install -y avahi-daemon
```

macOS resolves `.local` natively, so the deploy target becomes
`<you>@monolog-bot.local` with no router configuration. The alternative is a
DHCP reservation for a fixed LAN IP.

- [ ] From your laptop: `ping monolog-bot.local` resolves

## 2. Create the bot service user

```sh
sudo useradd --system --create-home --shell /usr/sbin/nologin monolog-bot
```

A system user keeps the bot out of regular login flow and gives systemd a
clean owner for the working tree. It is deliberately `nologin` — **it is not
the user you deploy as.** See step 8.

## 3. SSH deploy key for the tasks repo

```sh
sudo install -d -m 0750 -o monolog-bot -g monolog-bot /etc/monolog-bot
sudo -u monolog-bot ssh-keygen -t ed25519 -f /home/monolog-bot/.ssh/id_ed25519 -N ""
sudo install -m 0600 -o monolog-bot -g monolog-bot \
    /home/monolog-bot/.ssh/id_ed25519 /etc/monolog-bot/id_ed25519
cat /home/monolog-bot/.ssh/id_ed25519.pub
```

- [ ] Add the printed key to the tasks repo as a **deploy key with write
      access** — GitHub: Settings → Deploy keys → Add deploy key. Write
      access is required; the bot pushes after every mutation.
      With `gh`: `gh repo deploy-key add key.pub --repo <you>/monolog-tasks --allow-write`
- [ ] Keep this key separate from your laptop SSH key — rotating one does
      not invalidate the other

Note `/etc/monolog-bot` is created **here**, before anything is installed
into it.

## 4. Clone the tasks repo

**The clone must be owned by `monolog-bot`.** A clone made as root leaves the
service unable to write `.git/`, and every pull and push fails at runtime.

```sh
sudo -u monolog-bot \
  GIT_SSH_COMMAND="ssh -i /etc/monolog-bot/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
  git clone git@github.com:<you>/monolog-tasks.git /home/monolog-bot/tasks-repo
```

If you are running as root rather than through `sudo`, the equivalent is
`/usr/sbin/runuser -u monolog-bot -- env GIT_SSH_COMMAND="..." git clone ...`
— note the absolute path, `runuser` lives in `/usr/sbin` which is not on a
non-login shell's PATH.

- [ ] `sudo ls /home/monolog-bot/tasks-repo/.monolog/tasks | wc -l` matches
      your task count
- [ ] `sudo ls -ld /home/monolog-bot/tasks-repo/.git` shows
      `monolog-bot monolog-bot`

## 5. Telegram config block

The bot reads `<MONOLOG_DIR>/.monolog/config.json`, which is **inside the
tasks repo** — so you can set this on your laptop and let the bot pick it up
on its next pull, or edit it on the host directly.

```json
"telegram": {
  "enabled": true,
  "allowed_user_ids": [123456789],
  "pull_interval_seconds": 30,
  "browse_limit": 20
}
```

`pull_interval_seconds` is the **background** pull ticker — the safety net
that keeps the clone fresh and clears a read-only state while nobody is
sending commands. It is not what determines how quickly the bot sees a
laptop change: commands that read or act on existing state pull first
(rate-limited to one fetch per 5 seconds, shared with the ticker's clock),
so lowering this value buys little. 30s is a fine default.

- [ ] `enabled: true` and your own numeric Telegram user ID in
      `allowed_user_ids` — without both, the bot starts and silently ignores
      every message. DM [@userinfobot](https://t.me/userinfobot) for your ID.
- [ ] If set on the laptop: commit, push, then pull on the host
- [ ] Verify with `monolog telegram status` — it should echo your values,
      not the defaults

## 6. Install the systemd unit

Copy this repo's `docs/deploy/monolog-bot.service` to the host, then:

```sh
sudo install -m 0644 monolog-bot.service /etc/systemd/system/monolog-bot.service
sudo systemctl daemon-reload
```

The unit's `ExecStart` points at `/opt/monolog-bot/monolog` — an arch-neutral
name, so the same unit works on x86 and ARM.

## 7. Write the environment file

Copy this repo's `docs/deploy/env.example` to the host, then:

```sh
sudo install -m 0600 -o monolog-bot -g monolog-bot env.example /etc/monolog-bot/env
sudo $EDITOR /etc/monolog-bot/env
```

- [ ] `MONOLOG_TELEGRAM_TOKEN` — the value @BotFather gave you
- [ ] `GIT_SSH_COMMAND` points at `/etc/monolog-bot/id_ed25519`
- [ ] `MONOLOG_DIR` matches the clone path from step 4
- [ ] All four of `GIT_AUTHOR_NAME` / `GIT_AUTHOR_EMAIL` /
      `GIT_COMMITTER_NAME` / `GIT_COMMITTER_EMAIL`. These are **required** —
      without them every commit fails with `fatal: empty ident name not
      allowed` and the bot replies "sync conflict, change not saved" to
      every message even though nothing is conflicting. All four are needed
      because git's commit plumbing reads the COMMITTER pair independently
      from the AUTHOR pair, and the bot user has no `~/.gitconfig` to fall
      back to. `ProtectHome=read-only` in the unit makes `git config
      --global` inconvenient, so env vars are the canonical fix.
- [ ] Re-verify `0600` perms (`stat /etc/monolog-bot/env`) — the token
      lives here

## 8. Deploy the binary

From your laptop, with this repo checked out. Set the target once in a
gitignored `.env.deploy` at the repo root:

```make
DEPLOY_HOST = <you>@monolog-bot.local
BOT_ARCH    = amd64
```

`DEPLOY_HOST` must be a **login user with sudo** — not `monolog-bot`, which
is `nologin`. `EC2_HOST` is still accepted as an alias, so an older
`.env.deploy` keeps working unchanged.

The deploy target needs a place to land, and passwordless sudo for exactly
the two commands it runs. On the host:

```sh
sudo install -d -m 0755 -o root -g root /opt/monolog-bot

sudo tee /etc/sudoers.d/monolog-deploy >/dev/null <<'EOF'
<you> ALL=(root) NOPASSWD: /usr/bin/install -m 0755 -o root -g root /tmp/monolog-linux-amd64 /opt/monolog-bot/monolog
<you> ALL=(root) NOPASSWD: /usr/bin/systemctl restart monolog-bot
<you> ALL=(root) NOPASSWD: /usr/bin/systemctl status --no-pager monolog-bot
EOF
sudo chmod 0440 /etc/sudoers.d/monolog-deploy
sudo visudo -c -f /etc/sudoers.d/monolog-deploy    # must print "parsed OK"
```

**sudoers matches on exact arguments.** The three rules above mirror what
`make deploy-bot` actually runs, character for character — including
`--no-pager` and the `amd64` in the temp path. Change `BOT_ARCH` and the
first rule needs the matching filename. Granting bare `/usr/bin/install` with
no arguments instead would be passwordless root, so don't.

Then, from the laptop:

```sh
make deploy-bot
```

This cross-compiles for `BOT_ARCH`, scps the binary, installs it as
`/opt/monolog-bot/monolog` owned `root:root` mode 0755 (the service user
needs only read+execute — a compromised bot must not be able to rewrite its
own binary), and restarts the unit.

## 9. First start

```sh
sudo systemctl enable --now monolog-bot
sudo systemctl status monolog-bot
sudo journalctl -u monolog-bot -f
```

- [ ] Status reports `active (running)`
- [ ] `systemctl is-enabled monolog-bot` reports **`enabled`** — `start`
      alone does not survive a reboot, and an always-on host that loses the
      bot on the first power cut is worse than useless
- [ ] `systemctl show monolog-bot -p NRestarts --value` stays at `0`
- [ ] No git errors in the journal

## 10. Smoke test from your phone

- [ ] `/start` returns the help message
- [ ] A plain message creates a task — this is the one that exercises the
      full write path (capture → `store.Create` → commit → push)
- [ ] Tapping Done on the returned card flips it to strike-through
- [ ] `/today` returns the expected list
- [ ] Replying to a card with text lands a note on that task
- [ ] Add a task on the laptop (`monolog add "smoke test"`), then send
      `/today` from the phone — it appears without you syncing anything by
      hand. This exercises laptop auto-push → remote → the bot's
      before-command pull, i.e. the whole round trip in one step.

## Updating

```sh
make deploy-bot
```

The bot loses long-poll position briefly during restart; Telegram queues
updates by `update_id`, so nothing is dropped.

## Troubleshooting

Indexed by the error you will actually see.

**`status=203/EXEC` in `systemctl status`**
The binary at `ExecStart` does not exist or is not executable. Run
`make deploy-bot`. Expected before your first deploy.

**`error: cannot open '.git/FETCH_HEAD': Permission denied`**
The clone is owned by root instead of `monolog-bot`, so the service cannot
write to it. Fix: `sudo chown -R monolog-bot:monolog-bot /home/monolog-bot/tasks-repo`

**Bot replies `⚠️ sync conflict, change not saved` to everything**
Usually not a real conflict — it is a failed `git commit`. Check all four
`GIT_AUTHOR_*` / `GIT_COMMITTER_*` values in `/etc/monolog-bot/env`.

**`monolog telegram status` shows `enabled: false` despite config.json**
You are on a build predating the fix where `status` never called
`config.Load`. Update the binary.

**Bot ignores your messages entirely, no error**
Your user ID is not in `allowed_user_ids`, or `enabled` is false.
Non-allow-listed users are silently dropped by design.

**`ssh: connect to host … port 22: Connection refused`**
Nothing is listening — `openssh-server` is not installed or not running.
"Refused" means the host is up and rejecting; a firewall would time out
instead. Fix: `sudo apt install -y openssh-server && sudo systemctl enable --now ssh`

**`Too many authentication failures` when connecting**
Your SSH client offers every key in your agent before trying the password,
exhausting sshd's `MaxAuthTries` (default 6). Add a host block to
`~/.ssh/config`:

```
Host monolog-bot monolog-bot.local
  HostName monolog-bot.local
  User <you>
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
```

**`Sorry, user <you> is not allowed to execute '/usr/bin/…'`**
The command does not match a sudoers rule *including its arguments*. Either
you are running a setup command not covered by the deploy rules (do one-time
setup as root via `su -` instead), or `BOT_ARCH` changed and the temp-path
in the rule no longer matches.

**`runuser: command not found`**
It lives in `/usr/sbin`, absent from a non-login shell's PATH. Use `su -`
(with the dash) or the absolute path `/usr/sbin/runuser`. Equivalent:
`su -s /bin/sh monolog-bot -c '…'` — the `-s` overrides the `nologin` shell.

**Bot is gone after a reboot**
`systemctl enable monolog-bot`. See step 9.

**`journalctl -u monolog-bot` shows "No entries"**
Your user is not in `adm` or `systemd-journal`. Use `sudo journalctl`, or
`sudo usermod -aG adm <you>`.

## Security notes

- **Bot token** lives only in `/etc/monolog-bot/env` (`0600`). Never
  committed; never logged by monolog; `telegram status` prints only whether
  the env var is set, never the value.
- **SSH deploy key** is host-local. If the host is compromised, rotate by
  removing the deploy key on GitHub and regenerating.
- **Binary is root-owned**, service user is unprivileged and `nologin`. The
  bot cannot modify its own executable.
- **Deploy sudo rules** are argument-exact and cover only install + restart +
  status. They are not a general sudo grant.
- **Allow-list filter** is the only auth on bot input. Updates from any user
  ID not in `allowed_user_ids` are silently dropped. If your user ID leaks,
  worst case is someone spamming captures into your backlog; rotate by
  editing `config.json` and restarting.
- **No inbound listener.** Long polling means no webhook endpoint and no
  open port. Outbound HTTPS only.
