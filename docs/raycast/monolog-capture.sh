#!/usr/bin/env bash

# @raycast.schemaVersion 1
# @raycast.title Save to monolog
# @raycast.mode compact
# @raycast.packageName Monolog
# @raycast.icon 📥
# @raycast.argument1 { "type": "text", "placeholder": "Title" }
# @raycast.argument2 { "type": "text", "placeholder": "Note", "optional": true }

# Make sure GUI launches see the monolog binary. Adjust paths if your Go bin
# or homebrew prefix is elsewhere.
# export PATH="$HOME/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"

"$HOME"/IdeaProjects/monolog/monolog add "$1" --body "$2" --tags raycast
