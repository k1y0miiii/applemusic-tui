#!/bin/sh

set -eu

PROGRAM=amtui
PREFIX=${HOME:?"HOME is not set"}/.local/bin
CONFIGURE_PATH=1

usage() {
	cat <<'EOF'
Usage: ./install.sh [options]

Build and install Apple Music TUI for the current user.

Options:
  --prefix DIR  Install into DIR instead of ~/.local/bin
  --no-path     Do not modify the shell PATH configuration
  -h, --help    Show this help

Installed commands:
  amtui
  applemusic
  applemusic-tui
EOF
}

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--prefix)
			[ "$#" -ge 2 ] || die "--prefix requires a directory"
			PREFIX=$2
			shift 2
			;;
		--no-path)
			CONFIGURE_PATH=0
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			die "unknown option: $1"
			;;
	esac
done

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
cd "$SCRIPT_DIR"
[ -f go.mod ] || die "run this script from the applemusicTUI source checkout"
command -v go >/dev/null 2>&1 || die "Go is required: https://go.dev/dl/"

OS=$(uname -s)
case "$OS" in
	Darwin)
		command -v clang >/dev/null 2>&1 ||
			die "Xcode Command Line Tools are required: xcode-select --install"
		;;
	Linux)
		;;
	*)
		die "unsupported operating system: $OS"
		;;
esac

BUILD_DIR=$(mktemp -d "${TMPDIR:-/tmp}/amtui-install.XXXXXX")
trap 'rm -rf "$BUILD_DIR"' EXIT HUP INT TERM

printf 'Building %s for %s with %s...\n' "$PROGRAM" "$OS" "$(go version)"
case "$OS" in
	Darwin)
		# Isolate the cache so runtime/cgo is compiled for the same deployment
		# target as the CoreAudio bridge (avoids thousands of linker warnings).
		MACOSX_DEPLOYMENT_TARGET=14.2 \
			GOCACHE="$BUILD_DIR/go-cache" \
			CGO_ENABLED=1 \
			go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/$PROGRAM" .
		;;
	Linux)
		CGO_ENABLED=0 \
			go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/$PROGRAM" .
		;;
esac

mkdir -p "$PREFIX"
if command -v install >/dev/null 2>&1; then
	install -m 0755 "$BUILD_DIR/$PROGRAM" "$PREFIX/$PROGRAM"
else
	cp "$BUILD_DIR/$PROGRAM" "$PREFIX/$PROGRAM"
	chmod 0755 "$PREFIX/$PROGRAM"
fi

for alias in applemusic applemusic-tui; do
	destination=$PREFIX/$alias
	if [ -e "$destination" ] && [ ! -L "$destination" ]; then
		die "$destination already exists and is not a symlink"
	fi
	ln -sfn "$PROGRAM" "$destination"
done

START_MARKER="# >>> amtui installer >>>"
END_MARKER="# <<< amtui installer <<<"

update_path_config() {
	config_file=$1
	path_line=$2
	config_dir=$(dirname "$config_file")
	mkdir -p "$config_dir"

	temp_file=$(mktemp "${TMPDIR:-/tmp}/amtui-shell.XXXXXX")
	if [ -f "$config_file" ]; then
		awk -v start="$START_MARKER" -v end="$END_MARKER" '
			$0 == start { managed = 1; next }
			$0 == end   { managed = 0; next }
			!managed    { print }
		' "$config_file" >"$temp_file"
	fi
	{
		if [ -s "$temp_file" ]; then
			printf '\n'
		fi
		printf '%s\n%s\n%s\n' "$START_MARKER" "$path_line" "$END_MARKER"
	} >>"$temp_file"
	mv "$temp_file" "$config_file"
	printf '%s' "$config_file"
}

CONFIG_FILE=
if [ "$CONFIGURE_PATH" -eq 1 ]; then
	if [ "$PREFIX" = "$HOME/.local/bin" ]; then
		sh_path_line='export PATH="$HOME/.local/bin:$PATH"'
		fish_path_line='fish_add_path -g "$HOME/.local/bin"'
	else
		escaped_prefix=$(printf '%s' "$PREFIX" | sed "s/'/'\\\\''/g")
		sh_path_line="export PATH='$escaped_prefix':\"\$PATH\""
		fish_path_line="fish_add_path -g '$escaped_prefix'"
	fi

	shell_name=$(basename "${SHELL:-sh}")
	case "$shell_name" in
		zsh)
			config_root=${ZDOTDIR:-$HOME}
			CONFIG_FILE=$(update_path_config \
				"$config_root/.zshrc" \
				"$sh_path_line")
			;;
		bash)
			CONFIG_FILE=$(update_path_config \
				"$HOME/.bashrc" \
				"$sh_path_line")
			;;
		fish)
			CONFIG_FILE=$(update_path_config \
				"$HOME/.config/fish/config.fish" \
				"$fish_path_line")
			;;
		*)
			CONFIG_FILE=$(update_path_config \
				"$HOME/.profile" \
				"$sh_path_line")
			;;
	esac
fi

printf '\nInstalled:\n'
printf '  %s\n' "$PREFIX/amtui"
printf '  %s\n' "$PREFIX/applemusic"
printf '  %s\n' "$PREFIX/applemusic-tui"

if [ -n "$CONFIG_FILE" ]; then
	printf '\nPATH updated in %s\n' "$CONFIG_FILE"
	printf 'Open a new terminal or run:\n  . "%s"\n' "$CONFIG_FILE"
fi

case "$OS" in
	Darwin)
	if [ ! -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ] &&
		! command -v google-chrome >/dev/null 2>&1 &&
		! command -v chromium >/dev/null 2>&1; then
		printf '\nWarning: install Google Chrome before running amtui.\n' >&2
	fi
	printf '\nOn first launch, allow System Audio Recording for the terminal/amtui.\n'
	;;
	Linux)
	if ! command -v google-chrome >/dev/null 2>&1 &&
		! command -v google-chrome-stable >/dev/null 2>&1 &&
		! command -v chromium >/dev/null 2>&1 &&
		! command -v chromium-browser >/dev/null 2>&1; then
		printf '\nWarning: install Google Chrome or Chromium with Widevine support.\n' >&2
	fi
	printf '\nLive visualizer requires pipewire-pulse or PulseAudio.\n'
	;;
esac

printf '\nRun: amtui\n'
