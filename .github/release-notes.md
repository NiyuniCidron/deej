## Installing

Download the `.flatpak` bundle below, then:

```shell
flatpak remote-add --if-not-exists --user flathub https://flathub.org/repo/flathub.flatpakrepo
flatpak install --user ./deej-*.flatpak
flatpak run io.github.niyunicidron.deej
```

The first command is only needed if you don't already have `org.freedesktop.Platform//24.08`;
the install pulls it from Flathub if it's missing.

## What to look at

- On the first run, if deej can't open your Arduino's serial device, it asks whether to add
  you to the group that owns it. The group it names should be a real group on **your**
  machine (`dialout`, `uucp` or `plugdev`, depending on the distribution) - never `nogroup`.
  Accepting it runs `pkexec usermod` on the host, so you'll be asked for your password, and
  you'll need to log out and back in afterwards.
- `config.yaml` and `logs/` live in `~/.config/deej`, so "Edit configuration" in the tray menu
  opens the same file deej is watching. Saving it reloads the mapping without a restart.
- If something doesn't work, `~/.config/deej/logs/deej-latest-run.log` is the first place to
  look.

## Known gaps

- Configuration is edited by hand in `config.yaml`; there is no settings GUI in this build.
- Volume control needs PulseAudio or PipeWire's Pulse compatibility layer.
