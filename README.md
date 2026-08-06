# applemusic-tui — Apple Music in your terminal, on macOS **and** Linux

**English** · [Русский](README.ru.md)

![Go 1.26](https://img.shields.io/badge/Go-1.26-555?style=flat-square&logo=go&logoColor=white)
![Platforms](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-555?style=flat-square)
![License: MIT](https://img.shields.io/badge/license-MIT-555?style=flat-square)

`amtui` is a terminal Apple Music player — a full TUI client with catalog
search, a live audio spectrum visualizer and synced lyrics, running on **Linux
as well as macOS**. It drives the official Apple Music web player inside a
hidden Chromium, so playback is the real thing. No DRM hacks: the browser plays
the audio legally, amtui just gives it a terminal face.

<p align="center"><img src="docs/media/demo.gif" alt="amtui — Apple Music in the terminal" width="800"></p>

## Why amtui

Terminal Apple Music clients are usually AppleScript remotes for the macOS
Music.app: Mac-only, and limited to steering an app that has to be running
anyway. amtui talks to Apple Music's **web** player through MusicKit instead,
which changes what is possible:

- **Linux, not just macOS.** The same Go binary runs on both. (Linux support is
  newer — see the note under [Requirements](#requirements).)
- **The full catalog, in the terminal.** Search songs, albums and playlists
  straight from MusicKit — no desktop app in the loop.
- **A real spectrum visualizer.** Actual system-audio PCM through an FFT, not a
  decorative animation.
- **Synced lyrics** from LRCLIB, with the album cover rendered next to them.
- **Your credentials stay out of the terminal.** You sign in inside a real
  browser window, so 2FA and captcha just work.

## Features

- **Full Apple Music catalog** — search songs, albums and playlists, plus your
  recently played, straight from MusicKit.
- **Queue** — jump to any track, append to queue, play next.
- **Recently played** — a cover grid under the queue: your last ten albums,
  playlists and singles as half-block artwork, arrow keys to pick, `↵` to play.
- **Live visualizer** — what is actually playing: system-audio PCM through a
  4096-sample Hann-window FFT into 32 bands (denser at low frequencies), 30 fps.
  CoreAudio process tap on macOS, PipeWire/PulseAudio monitor on Linux. Falls
  back to a clearly labeled simulated animation when live capture is
  unavailable. Three looks, cycled with `v`: Winamp-style **bars**, a spinning
  **torus** whose tube corrugates with the spectrum, and a wireframe **sphere**
  that turns slowly and pumps with the bass.
- **Synced lyrics** — timestamped lines from [LRCLIB](https://lrclib.net),
  no API keys, with the album cover rendered beside them in half-block color.
- **Themes** — six built-in palettes cycled with `t`, overridable per color in
  a config file, plus an `auto` mode that pulls the accent from the artwork.
- **Transport** — play/pause, next/prev, seek, volume, shuffle, repeat, and a
  progress bar drawn as the waveform of what you have already heard.
- **Safe sign-in** — you log in inside a real browser window; your Apple ID
  credentials never touch the terminal.

## How it works

Apple Music has no public streaming API, and grabbing the stream would mean
breaking DRM — off the table. Instead, the browser plays the music legally and
amtui remote-controls it:

```
┌──────────────┐   chromedp (CDP)   ┌───────────────────────────┐
│  amtui (Go)  │ ◄────────────────► │ hidden Chromium           │
│  Bubble Tea  │                    │ music.apple.com, signed in│
└──────────────┘                    │ window.MusicKit (JS API)  │
                                    └───────────────────────────┘
```

One Go binary. It spawns a hidden Chromium with a persistent profile in
`~/.config/amtui/chrome` and talks to `window.MusicKit` in page context —
search, queue, play/pause, seek, now playing. No DOM scraping. Audio goes out
through the system mixer (the web player serves AAC 256; no lossless).

<a id="requirements"></a>

## Requirements

- An active **Apple Music subscription**
- **Go 1.26+** (to build from source)
- **Chrome or Chromium**
  - macOS: any recent Chrome/Chromium
  - Linux: a Widevine-capable browser — `google-chrome`, or Chromium with the
    Widevine plugin (e.g. `chromium-widevine` on the AUR)
- **macOS 14.2+** (the live visualizer uses a CoreAudio process tap),
  or **Linux** with `pipewire-pulse` (or plain PulseAudio) for live capture

> Linux support is **experimental** — designed for Arch/Ubuntu, currently less
> tested than macOS. On Hyprland the browser window is auto-hidden into a
> special workspace (Wayland forbids offscreen positioning); other Wayland
> compositors may leave the window visible for now.

> Windows binaries are **untested and incomplete**: they build and the player
> works, but there is no system-audio capture backend on Windows, so the
> visualizer always runs in its labeled simulated mode. Use Windows Terminal —
> the legacy console does not render the TUI correctly. Reports welcome.

## Install

### Prebuilt binaries

Grab the archive for your platform from the
[latest release](https://github.com/k1y0miiii/applemusic-tui/releases/latest) —
macOS (Apple Silicon / Intel), Linux (x86-64 / arm64) and Windows
(x86-64 / arm64):

```sh
tar -xzf amtui-*-linux-amd64.tar.gz
sudo install -m755 amtui-*/amtui /usr/local/bin/amtui
amtui --version
```

Every release ships a `SHA256SUMS` file; verify with `shasum -a 256 -c SHA256SUMS`.

macOS binaries are unsigned, so the first launch needs
`xattr -d com.apple.quarantine amtui` (or right-click → Open).

### From source

```sh
git clone https://github.com/k1y0miiii/applemusic-tui
cd applemusic-tui
./install.sh
```

The script builds from source and installs `amtui` (plus `applemusic` and
`applemusic-tui` aliases) into `~/.local/bin`, adding it to PATH for
zsh / bash / fish. `--prefix DIR` installs elsewhere, `--no-path` leaves your
shell config alone. If Chrome is missing, the installer offers to install it
(Homebrew on macOS, the official `.deb` or AUR on Linux).

On macOS the live visualizer needs the Xcode Command Line Tools
(`xcode-select --install`) — the installer checks for them.

Prefer doing it by hand? `go build -o amtui .` works too; a `CGO_ENABLED=0`
build runs the visualizer in simulated mode instead of capturing real audio.

Run the test suite with `make test`, or `make verify` for the full check.

> Building all release archives yourself: `make dist`. macOS is the only host
> that produces a complete set — the darwin builds need cgo for the CoreAudio
> visualizer, while the Linux and Windows targets are pure Go and
> cross-compile from anywhere.

## First run

1. Launch `./amtui` — a **visible** browser window opens on music.apple.com.
2. Sign in with your Apple ID. It is a normal browser, so 2FA and captcha
   just work.
3. Done — the window hides and the TUI takes over. The session persists in
   `~/.config/amtui/chrome`, so next launches go straight to the player.


## Keys

### Player

| Key | Action |
| --- | --- |
| `Space` | Play / pause |
| `n` / `p` | Next / previous track |
| `Tab` | Cycle focus: queue → recently played → transport |
| `j` `k` / `↓` `↑` | Queue: move selection · Recent: move a grid row · Transport: volume down / up |
| `Enter` | Play the selected queue track or recently-played cover |
| `←` / `→` | Recent: previous / next cover · Transport: seek −5 s / +5 s |
| `s` | Toggle shuffle |
| `r` | Cycle repeat mode |
| `v` | Cycle visualizer: bars → torus → sphere |
| `t` | Cycle color theme |
| `R` | Reload the web player if it wedges |
| `/` | Open search |
| `q` / `Ctrl+C` | Quit |

### Search

| Key | Action |
| --- | --- |
| typing | Edit the query |
| `Enter` | In input: search (empty query — your library) · In list: play selection |
| `Tab` | Next tab: RECENT / SONGS / ALBUMS / PLAYLISTS |
| `↓` `↑` / `j` `k` | Move through results (`↓` from the input dives into the list) |
| `a` / `A` | Add to the end of the queue / play next |
| `Esc` | Close search |

<p align="center"><img src="docs/media/search.png" alt="Search overlay" width="800"></p>
<p align="center"><img src="docs/media/lyrics.png" alt="Queue, live visualizer and synced lyrics" width="800"></p>

## Visualizer

Press `v` to cycle three shapes; the choice is remembered in
`~/.config/amtui/vizmode`.

| Mode | What it draws |
| --- | --- |
| `bars` | 32-band spectrum with peak-hold markers. The default. |
| `torus` | A spinning donut. Its tube thickness at each angle comes from the band that owns that slice of the ring, so the shape corrugates with the spectrum. |
| `sphere` | A wireframe globe of latitude rings and meridians, shaded by depth. Frequency maps to latitude symmetrically, so the bass swells its waist. |

The two 3D shapes answer the beat differently. The torus answers with motion: a
bass kick speeds its spin up. The sphere answers with size — it turns at about a
third of that pace and instead swells and contracts on the bass, so the pump is
what you watch rather than the rotation.

The bars do neither; they are the spectrum itself. Independent of the mode, the
panel borders pulse with the bass — `visualizer.pulse` turns that off.

Both 3D shapes are plain ASCII over the same 32 bands the bars use, so they cost
no extra audio work and follow the active theme's accent colors.

## Themes

Six built-in themes: `apple` (default), `catppuccin`, `gruvbox`, `nord`, `mono`
and `auto`. Press `t` to cycle them; the choice is remembered in
`~/.config/amtui/theme`.

`auto` takes its accent from the dominant color of the current track's album
art. Only the accent changes — background and text stay neutral, so no cover can
make the interface unreadable.

To pick a theme up front or override individual colors, copy
[`docs/config.example.toml`](docs/config.example.toml) to
`~/.config/amtui/config.toml`. The same file switches the peak markers, the bass
pulse, the waveform progress bar and the album art on or off.

## Configuration

| Environment variable | Effect |
| --- | --- |
| `AMTUI_CHROME` | Path to the Chrome/Chromium binary (overrides auto-detection) |
| `AMTUI_CONFIG_DIR` | Config directory (default `~/.config/amtui`) |
| `AMTUI_DEBUG` | Run the browser visibly, with verbose logging |

## License

[MIT](LICENSE)
