# Monolog Telegram Bot — EC2 Deployment

One-time setup for running `monolog telegram serve` on an always-on EC2
t4g.nano. Each numbered step is meant to be ticked off as you go.

The bot is a normal monolog client: it owns a clone of the tasks git repo
and uses the same `store` + `git` paths the CLI uses. Once running, the
laptop side gets no new wiring — `s` sync and the fsnotify watcher already
handle the read side.

## 1. Launch the instance

- [ ] EC2 t4g.nano, Amazon Linux 2023 ARM64 (matches `GOARCH=arm64`)
- [ ] Security group: inbound SSH from your IP only; outbound HTTPS open
      (Telegram API + GitHub clone/pull/push). No public listening port —
      the bot uses long polling.
- [ ] Allocate / associate an Elastic IP if you want a stable SSH target

## 2. Create the bot user on the host

```sh
sudo useradd --system --create-home --shell /usr/sbin/nologin monolog-bot
```

A system user keeps the bot out of regular login flow and gives systemd a
clean owner for the working tree.

## 3. SSH deploy key for the tasks repo

On the EC2 host, as the bot user:

```sh
sudo -u monolog-bot ssh-keygen -t ed25519 -f /home/monolog-bot/.ssh/id_ed25519 -N ""
sudo install -m 0600 -o monolog-bot -g monolog-bot \
    /home/monolog-bot/.ssh/id_ed25519 /etc/monolog-bot/id_ed25519
sudo install -m 0644 -o monolog-bot -g monolog-bot \
    /home/monolog-bot/.ssh/id_ed25519.pub /etc/monolog-bot/id_ed25519.pub
sudo cat /etc/monolog-bot/id_ed25519.pub
```

- [ ] Copy the printed public key
- [ ] Add it to the tasks-repo on GitHub as a **deploy key with write
      access** (Settings → Deploy keys → Add deploy key). Write access is
      required because the bot pushes after every mutation.
- [ ] Keep this key separate from your laptop SSH key — rotating one does
      not invalidate the other.

## 4. Initial git clone

```sh
sudo -u monolog-bot GIT_SSH_COMMAND="ssh -i /etc/monolog-bot/id_ed25519 -o StrictHostKeyChecking=no" \
    git clone git@github.com:<you>/<tasks-repo>.git /home/monolog-bot/tasks-repo
```

- [ ] Verify `/home/monolog-bot/tasks-repo/.monolog/tasks/` exists after
      the clone
- [ ] Decide whether to populate `<repo>/.monolog/config.json`'s
      `"telegram"` block here on the laptop side and let the bot pick it up
      via pull, or edit it on the EC2 host directly. Either way, you need
      at least `enabled: true` and your own Telegram user ID in
      `allowed_user_ids` before the bot will accept input from you. (DM
      [@userinfobot](https://t.me/userinfobot) to find your numeric ID.)

## 5. Install the systemd unit

Copy this repo's `docs/deploy/monolog-bot.service` to
`/etc/systemd/system/monolog-bot.service` on the host:

```sh
sudo install -m 0644 monolog-bot.service /etc/systemd/system/monolog-bot.service
sudo systemctl daemon-reload
```

## 6. Write the environment file

```sh
sudo install -d -m 0750 -o monolog-bot -g monolog-bot /etc/monolog-bot
sudo install -m 0600 -o monolog-bot -g monolog-bot env.example /etc/monolog-bot/env
sudo $EDITOR /etc/monolog-bot/env
```

- [ ] Set `MONOLOG_TELEGRAM_TOKEN` to the value @BotFather gave you
- [ ] Confirm `GIT_SSH_COMMAND` points at `/etc/monolog-bot/id_ed25519`
- [ ] Confirm `MONOLOG_DIR` matches the clone path from step 4
- [ ] Re-verify `0600` perms (`stat /etc/monolog-bot/env`) — the secret
      lives here

## 7. First start

```sh
sudo systemctl enable --now monolog-bot
sudo systemctl status monolog-bot
sudo journalctl -u monolog-bot -f
```

- [ ] Status reports `active (running)`
- [ ] `journalctl` shows the bot warming up and no auth errors

## 8. Smoke test from your phone

- [ ] DM the bot `/start` and confirm you get the help message
- [ ] Send a plain message — a task should appear on the laptop after
      `monolog sync`
- [ ] Tap the Done button on the returned summary card — task flips to
      done with the strike-through
- [ ] `/today` returns the expected list
- [ ] Reply to the bot's summary message with text — a note should land
      on the task

## 9. Deploying updates

From your laptop, with this repo checked out:

```sh
make deploy-bot EC2_HOST=ec2-user@<elastic-ip>
```

This cross-compiles to `linux/arm64`, scps the binary to the host, and
restarts the systemd unit. The bot loses long-poll position briefly during
restart; Telegram queues updates by `update_id` so nothing is dropped.

## Security notes

- **Bot token** lives only in `/etc/monolog-bot/env` (`0600`). Never
  committed; never logged by monolog; `telegram status` redacts the value
  and prints only whether the env var is set.
- **SSH deploy key** is EC2-local. If the host is compromised, rotate by
  removing the deploy key on GitHub and regenerating on a fresh instance.
- **Allow-list filter** is the only auth on bot input. Updates from any
  user ID not in `allowed_user_ids` are silently dropped. If your user ID
  ever leaks (unlikely — it's not secret, but it's not advertised either),
  the worst case is someone else can spam capture into your backlog;
  rotate by editing `config.json` and restarting.
