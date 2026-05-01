# Quick capture into monolog (Raycast)

Two-second capture from anywhere on macOS — browser pages (with URL + selection-aware text fragments) or selected text in any app — into a monolog task tagged `inbox` and scheduled for today.

The whole flow is: bookmarklet (or Raycast hotkey) → Raycast popup with title + note prefilled → Enter → `monolog add --body ... --tags inbox`. No daemon, no extra binary, no URL-scheme registration.

## Setup

### 1. Install the Raycast script command

Either symlink or copy `monolog-capture.sh` from this directory into your Raycast script-commands folder, e.g.:

```sh
ln -s "$PWD/docs/raycast/monolog-capture.sh" ~/.config/raycast/script-commands/
```

If you don't have a script-commands folder yet, point Raycast at one in **Raycast → Settings → Extensions → Script Commands → "+" → Add Directory**.

If your `monolog` binary lives somewhere unusual, edit the `PATH` line in the script. If you use a non-default `MONOLOG_DIR`, uncomment and set it inside the script (GUI launches don't read your shell rc files; alternatively run `launchctl setenv MONOLOG_DIR /path/to/dir` once per login).

### 2. Assign a global hotkey (for desktop apps)

In Raycast settings, search for "Save to monolog" and assign a hotkey (e.g. ⌃⌥⌘N). When you trigger it from any app, Raycast pops the form. If you want it to prefill from the current selection, enable Raycast's "Prefill input with selected text" for the command.

### 3. Drag the bookmarklet into your bookmarks bar

Create a bookmark with this URL (paste the full thing as the URL):

```js
javascript:(function(){var s=window.getSelection().toString().trim();var u;if(s){var b=location.origin+location.pathname+location.search;u=b+'#:~:text='+encodeURIComponent(s.slice(0,300));}else{u=location.href;}var t=s?s.slice(0,80):document.title;var n=s?s+'\n\n'+u:u;location.href='raycast://script-commands/monolog-capture?arguments='+encodeURIComponent(JSON.stringify({argument1:t,argument2:n}));})();
```

Most browsers won't let you paste `javascript:` directly into a new bookmark via the URL field — easiest path is: bookmark any page, then edit it and paste the snippet into the URL field.

## Behavior

| Trigger | Title prefill | Note prefill |
|---|---|---|
| Bookmarklet, no selection | page title | page URL |
| Bookmarklet, with selection | selection (truncated to 80 chars) | full selection + URL with `#:~:text=` fragment that scrolls to and highlights the original passage when reopened |
| Hotkey from a desktop app | selected text (if Raycast prefill enabled), else empty | empty |

Both fields are editable in the Raycast popup before you hit Enter. The task lands on today's list with the `inbox` tag — filter on that tag to triage later.

## Defaults

The script hard-codes `--tags inbox` and relies on `monolog add`'s default schedule (today). Change either by editing the script — `monolog add` accepts:

- `-s <bucket-or-date>` — tomorrow, week, month, someday, or a date in your configured format
- `-t <tag1,tag2>` — comma-separated tags
- `-b <body>` — body text (already used)

## Troubleshooting

- **"command not found: monolog"** — your GUI PATH doesn't include where `monolog` lives. Edit the `export PATH=...` line in the script.
- **Tasks land in the wrong repo** — set `MONOLOG_DIR` inside the script, or run `launchctl setenv MONOLOG_DIR /path` once.
- **Bookmarklet does nothing** — confirm Raycast is running and the script command shows up in Raycast's command list. If the script doesn't appear, Raycast hasn't indexed the directory yet — open Raycast settings and verify the script-commands directory.
- **Text fragment doesn't scroll on reopen** — Chrome/Edge support text fragments natively; Firefox supports them since 131. Some sites with aggressive client-side rendering won't match. Falls back to opening the page top.
