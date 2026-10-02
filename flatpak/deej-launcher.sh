#!/bin/sh
# deej reads config.yaml and writes logs/ relative to its working directory, so give it a
# stable, writable directory instead of wherever the desktop happened to launch it from.
# ~/.config/deej is bind-mounted at the same path inside and outside the sandbox, so a host
# text editor opens the exact file deej is watching.
set -e

CONFIG_DIR="$HOME/.config/deej"
mkdir -p "$CONFIG_DIR"

if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    cp /app/share/deej/config.yaml "$CONFIG_DIR/config.yaml"
fi

cd "$CONFIG_DIR"

exec /app/bin/deej-bin "$@"
