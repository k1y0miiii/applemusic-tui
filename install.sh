#!/bin/sh

set -eu

PROGRAM=amtui
PREFIX=
PREFIX_WAS_SET=0
CONFIGURE_PATH=1
START_MARKER="# >>> amtui installer >>>"
END_MARKER="# <<< amtui installer <<<"

BUILD_DIR=
CACHE_PROBE=
INSTALL_TEMP=
SHELL_TEMP=
TEMP_GOMODCACHE=

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

cleanup() {
	cleanup_status=$?
	trap - 0 HUP INT TERM
	set +e

	if [ -n "$CACHE_PROBE" ]; then
		rm -f "$CACHE_PROBE" 2>/dev/null
	fi
	if [ -n "$SHELL_TEMP" ]; then
		rm -f "$SHELL_TEMP" 2>/dev/null
	fi
	if [ -n "$INSTALL_TEMP" ]; then
		rm -f "$INSTALL_TEMP" 2>/dev/null
	fi
	if [ -n "$TEMP_GOMODCACHE" ] && [ -n "$BUILD_DIR" ]; then
		case "$TEMP_GOMODCACHE" in
			"$BUILD_DIR"/*)
				chmod -R u+w "$TEMP_GOMODCACHE" 2>/dev/null
				;;
		esac
	fi
	if [ -n "$BUILD_DIR" ]; then
		rm -rf "$BUILD_DIR" 2>/dev/null
	fi

	exit "$cleanup_status"
}

trap cleanup 0
trap 'exit 1' HUP INT TERM

while [ "$#" -gt 0 ]; do
	case "$1" in
		--prefix)
			[ "$#" -ge 2 ] || die "--prefix requires a directory"
			PREFIX=$2
			PREFIX_WAS_SET=1
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

if [ "$PREFIX_WAS_SET" -eq 0 ]; then
	[ -n "${HOME:-}" ] ||
		die "HOME is not set; use --prefix DIR to choose an install directory"
	PREFIX=$HOME/.local/bin
fi
[ -n "$PREFIX" ] || die "--prefix requires a nonempty directory"

SCRIPT_DIR=$(CDPATH='' cd "$(dirname "$0")" && pwd)
cd "$SCRIPT_DIR"
[ -f go.mod ] || die "run this script from the applemusicTUI source checkout"

RESOLVED_CONFIG=

resolve_config_target() {
	RESOLVED_CONFIG=$1
	link_count=0
	while [ -L "$RESOLVED_CONFIG" ]; do
		if [ "$link_count" -ge 40 ]; then
			die "too many symlinks while resolving $1"
		fi
		if link_target=$(readlink "$RESOLVED_CONFIG"); then
			:
		else
			die "cannot read shell config symlink: $RESOLVED_CONFIG"
		fi
		case "$link_target" in
			/*)
				RESOLVED_CONFIG=$link_target
				;;
			*)
				RESOLVED_CONFIG=$(dirname "$RESOLVED_CONFIG")/$link_target
				;;
		esac
		link_count=$((link_count + 1))
	done
}

validate_path_config() {
	config_file=$1
	resolve_config_target "$config_file"
	edit_file=$RESOLVED_CONFIG

	if [ -e "$edit_file" ] && [ ! -f "$edit_file" ]; then
		die "$config_file exists but is not a regular file"
	fi
	if [ ! -f "$edit_file" ]; then
		return
	fi

	if awk -v start="$START_MARKER" -v end="$END_MARKER" '
		$0 == start {
			if (managed) invalid = 1
			managed = 1
			next
		}
		$0 == end {
			if (!managed) invalid = 1
			managed = 0
			next
		}
		END {
			if (managed || invalid) exit 42
		}
	' "$edit_file" >/dev/null; then
		:
	else
		validation_status=$?
		if [ "$validation_status" -eq 42 ]; then
			die "$config_file has an unterminated or malformed amtui managed block"
		fi
		die "cannot safely read shell config: $config_file"
	fi
}

update_path_config() {
	config_file=$1
	path_block=$2
	validate_path_config "$config_file"
	resolve_config_target "$config_file"
	edit_file=$RESOLVED_CONFIG
	config_dir=$(dirname "$edit_file")
	mkdir -p "$config_dir" ||
		die "cannot create shell config directory: $config_dir"

	if SHELL_TEMP=$(mktemp "$config_dir/.amtui-shell.XXXXXX"); then
		:
	else
		die "cannot create a temporary shell config next to $edit_file"
	fi
	if [ -f "$edit_file" ]; then
		if awk -v start="$START_MARKER" -v end="$END_MARKER" '
			$0 == start { managed = 1; next }
			$0 == end   { managed = 0; next }
			!managed    { print }
		' "$edit_file" >"$SHELL_TEMP"; then
			:
		else
			die "cannot update shell config: $config_file"
		fi
	fi
	shell_temp_has_content=0
	if [ -s "$SHELL_TEMP" ]; then
		shell_temp_has_content=1
	fi
	if {
		if [ "$shell_temp_has_content" -eq 1 ]; then
			printf '\n'
		fi
		printf '%s\n%s\n%s\n' "$START_MARKER" "$path_block" "$END_MARKER"
	} >>"$SHELL_TEMP"; then
		:
	else
		die "cannot write temporary shell config for $config_file"
	fi
	if mv -f "$SHELL_TEMP" "$edit_file"; then
		SHELL_TEMP=
	else
		die "cannot replace shell config target: $edit_file"
	fi
}

preflight_destinations() {
	if { [ -e "$PREFIX" ] || [ -L "$PREFIX" ]; } && [ ! -d "$PREFIX" ]; then
		die "$PREFIX already exists and is not a directory"
	fi

	main_destination=$PREFIX/$PROGRAM
	if [ -L "$main_destination" ]; then
		die "$main_destination already exists as a symlink; refusing to overwrite it"
	fi
	if [ -e "$main_destination" ] && [ ! -f "$main_destination" ]; then
		die "$main_destination already exists and is not a regular file"
	fi

	for alias in applemusic applemusic-tui; do
		destination=$PREFIX/$alias
		if [ -e "$destination" ] && [ ! -L "$destination" ]; then
			die "$destination already exists and is not a symlink"
		fi
	done
}

quote_shell_word() {
	quoted_value=$(printf '%s' "$1" | sed "s/'/'\\\\''/g")
	printf "'%s'" "$quoted_value"
}

CONFIG_FILE_1=
CONFIG_BLOCK_1=
CONFIG_FILE_2=
CONFIG_BLOCK_2=
SOURCE_STYLE=posix

if [ "$CONFIGURE_PATH" -eq 1 ]; then
	if [ -n "${HOME:-}" ] && [ "$PREFIX" = "$HOME/.local/bin" ]; then
		# These variables must expand when the user's shell loads the block.
		# shellcheck disable=SC2016
		sh_path_block='case ":$PATH:" in
	*:"$HOME/.local/bin":*) ;;
	*) export PATH="$HOME/.local/bin:$PATH" ;;
esac'
		# shellcheck disable=SC2016
		fish_path_block='fish_add_path -g "$HOME/.local/bin"'
	else
		escaped_prefix=$(printf '%s' "$PREFIX" | sed "s/'/'\\\\''/g")
		sh_path_block="case \":\$PATH:\" in
	*:'$escaped_prefix':*) ;;
	*) export PATH='$escaped_prefix':\"\$PATH\" ;;
esac"
		fish_path_block="fish_add_path -g '$escaped_prefix'"
	fi

	shell_path=${SHELL:-sh}
	shell_name=${shell_path##*/}
	[ -n "$shell_name" ] || shell_name='sh'
	case "$shell_name" in
		zsh)
			config_root=${ZDOTDIR:-${HOME:-}}
			[ -n "$config_root" ] ||
				die "HOME or ZDOTDIR is required to configure zsh PATH"
			CONFIG_FILE_1=$config_root/.zshrc
			CONFIG_BLOCK_1=$sh_path_block
			;;
		bash)
			[ -n "${HOME:-}" ] || die "HOME is required to configure bash PATH"
			CONFIG_FILE_1=$HOME/.bashrc
			CONFIG_BLOCK_1=$sh_path_block
			if [ -e "$HOME/.bash_profile" ] || [ -L "$HOME/.bash_profile" ]; then
				CONFIG_FILE_2=$HOME/.bash_profile
			elif [ -e "$HOME/.profile" ] || [ -L "$HOME/.profile" ]; then
				CONFIG_FILE_2=$HOME/.profile
			else
				CONFIG_FILE_2=$HOME/.bash_profile
			fi
			CONFIG_BLOCK_2=$sh_path_block
			;;
		fish)
			[ -n "${HOME:-}" ] || die "HOME is required to configure fish PATH"
			CONFIG_FILE_1=$HOME/.config/fish/config.fish
			CONFIG_BLOCK_1=$fish_path_block
			SOURCE_STYLE=fish
			;;
		*)
			[ -n "${HOME:-}" ] || die "HOME is required to configure shell PATH"
			CONFIG_FILE_1=$HOME/.profile
			CONFIG_BLOCK_1=$sh_path_block
			;;
	esac
fi

preflight_destinations
if [ -n "$CONFIG_FILE_1" ]; then
	validate_path_config "$CONFIG_FILE_1"
fi
if [ -n "$CONFIG_FILE_2" ]; then
	validate_path_config "$CONFIG_FILE_2"
fi

command -v go >/dev/null 2>&1 || die "Go is required: https://go.dev/dl/"
if OS=$(uname -s); then
	:
else
	die "cannot determine the operating system"
fi
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

if GO_VERSION=$(go version); then
	:
else
	die "go version failed; check the Go installation"
fi

if BUILD_DIR=$(mktemp -d "${TMPDIR:-/tmp}/amtui-install.XXXXXX"); then
	:
else
	die "cannot create a temporary build directory"
fi

CONFIGURED_GOMODCACHE=${GOMODCACHE:-}
if [ -z "$CONFIGURED_GOMODCACHE" ]; then
	if CONFIGURED_GOMODCACHE=$(go env GOMODCACHE); then
		:
	else
		die "cannot determine GOMODCACHE"
	fi
fi
if [ -n "$CONFIGURED_GOMODCACHE" ] && [ -e "$CONFIGURED_GOMODCACHE" ]; then
	module_cache_writable=0
	if [ -d "$CONFIGURED_GOMODCACHE" ]; then
		if CACHE_PROBE=$(mktemp "$CONFIGURED_GOMODCACHE/.amtui-write.XXXXXX" 2>/dev/null); then
			if rm -f "$CACHE_PROBE"; then
				CACHE_PROBE=
				module_cache_writable=1
			else
				die "cannot remove GOMODCACHE write probe: $CACHE_PROBE"
			fi
		fi
	fi
	if [ "$module_cache_writable" -eq 0 ]; then
		TEMP_GOMODCACHE=$BUILD_DIR/go-mod-cache
		mkdir -p "$TEMP_GOMODCACHE" ||
			die "cannot create temporary module cache"
		GOMODCACHE=$TEMP_GOMODCACHE
		export GOMODCACHE
		printf 'Configured GOMODCACHE is not writable; using temporary module cache: %s\n' \
			"$TEMP_GOMODCACHE"
	fi
fi

printf 'Building %s for %s with %s...\n' "$PROGRAM" "$OS" "$GO_VERSION"
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

mkdir -p "$PREFIX" || die "cannot create install directory: $PREFIX"
if INSTALL_TEMP=$(mktemp "$PREFIX/.amtui-install.XXXXXX"); then
	:
else
	die "cannot create a temporary install file in $PREFIX"
fi
if command -v install >/dev/null 2>&1; then
	install -m 0755 "$BUILD_DIR/$PROGRAM" "$INSTALL_TEMP" ||
		die "cannot stage $PROGRAM in $PREFIX"
else
	cp "$BUILD_DIR/$PROGRAM" "$INSTALL_TEMP" ||
		die "cannot stage $PROGRAM in $PREFIX"
	chmod 0755 "$INSTALL_TEMP" ||
		die "cannot make staged $PROGRAM executable"
fi

# Recheck immediately before committing to avoid following a destination
# changed after the initial preflight.
preflight_destinations
mv -f "$INSTALL_TEMP" "$PREFIX/$PROGRAM" ||
	die "cannot atomically install $PROGRAM"
INSTALL_TEMP=
ln -sfn "$PROGRAM" "$PREFIX/applemusic" ||
	die "cannot install applemusic alias"
ln -sfn "$PROGRAM" "$PREFIX/applemusic-tui" ||
	die "cannot install applemusic-tui alias"

if [ -n "$CONFIG_FILE_1" ]; then
	update_path_config "$CONFIG_FILE_1" "$CONFIG_BLOCK_1"
fi
if [ -n "$CONFIG_FILE_2" ]; then
	update_path_config "$CONFIG_FILE_2" "$CONFIG_BLOCK_2"
fi

printf '\nInstalled:\n'
printf '  %s\n' "$PREFIX/amtui"
printf '  %s\n' "$PREFIX/applemusic"
printf '  %s\n' "$PREFIX/applemusic-tui"

if [ -n "$CONFIG_FILE_1" ]; then
	printf '\nPATH updated in:\n'
	printf '  %s\n' "$CONFIG_FILE_1"
	if [ -n "$CONFIG_FILE_2" ]; then
		printf '  %s\n' "$CONFIG_FILE_2"
	fi
	printf 'Open a new terminal or run:\n'
	quoted_config=$(quote_shell_word "$CONFIG_FILE_1")
	if [ "$SOURCE_STYLE" = fish ]; then
		printf '  source %s\n' "$quoted_config"
	else
		printf '  . %s\n' "$quoted_config"
	fi
	if [ -n "$CONFIG_FILE_2" ]; then
		quoted_config=$(quote_shell_word "$CONFIG_FILE_2")
		printf '  . %s\n' "$quoted_config"
	fi
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
