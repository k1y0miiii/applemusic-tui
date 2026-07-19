package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScriptInstallsAliasesAndConfiguresShellPath(t *testing.T) {
	tests := []struct {
		name       string
		shell      string
		configPath string
		pathLine   string
	}{
		{
			name:       "zsh",
			shell:      "/bin/zsh",
			configPath: ".zshrc",
			pathLine:   `export PATH="$HOME/.local/bin:$PATH"`,
		},
		{
			name:       "bash",
			shell:      "/bin/bash",
			configPath: ".bashrc",
			pathLine:   `export PATH="$HOME/.local/bin:$PATH"`,
		},
		{
			name:       "fish",
			shell:      "/usr/bin/fish",
			configPath: ".config/fish/config.fish",
			pathLine:   `fish_add_path -g "$HOME/.local/bin"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			fakeBin := filepath.Join(home, "fake-bin")
			if err := os.MkdirAll(fakeBin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFakeGo(t, filepath.Join(fakeBin, "go"))

			env := append(os.Environ(),
				"HOME="+home,
				"SHELL="+tt.shell,
				"ZDOTDIR=",
				"PATH="+fakeBin+":/usr/bin:/bin:/usr/sbin:/sbin",
			)
			for range 2 {
				cmd := exec.Command("sh", "./install.sh")
				cmd.Env = env
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("install.sh failed: %v\n%s", err, output)
				}
			}

			installDir := filepath.Join(home, ".local", "bin")
			mainBinary := filepath.Join(installDir, "amtui")
			info, err := os.Stat(mainBinary)
			if err != nil {
				t.Fatalf("installed binary: %v", err)
			}
			if info.Mode()&0o111 == 0 {
				t.Fatalf("installed binary mode = %v, want executable", info.Mode())
			}
			for _, alias := range []string{"applemusic", "applemusic-tui"} {
				target, err := os.Readlink(filepath.Join(installDir, alias))
				if err != nil {
					t.Fatalf("%s alias: %v", alias, err)
				}
				if target != "amtui" {
					t.Fatalf("%s -> %q, want amtui", alias, target)
				}
			}

			config, err := os.ReadFile(filepath.Join(home, tt.configPath))
			if err != nil {
				t.Fatalf("shell config: %v", err)
			}
			text := string(config)
			if count := strings.Count(text, "# >>> amtui installer >>>"); count != 1 {
				t.Fatalf("managed PATH block count = %d, want 1\n%s", count, text)
			}
			if !strings.Contains(text, tt.pathLine) {
				t.Fatalf("shell config lacks %q:\n%s", tt.pathLine, text)
			}
		})
	}
}

func TestInstallScriptSupportsCustomPrefixWithoutPathMutation(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fake-bin")
	prefix := filepath.Join(home, "custom", "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGo(t, filepath.Join(fakeBin, "go"))

	cmd := exec.Command("sh", "./install.sh", "--prefix", prefix, "--no-path")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"SHELL=/bin/zsh",
		"ZDOTDIR=",
		"PATH="+fakeBin+":/usr/bin:/bin:/usr/sbin:/sbin",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(prefix, "amtui")); err != nil {
		t.Fatalf("custom-prefix binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf("--no-path created shell config: %v", err)
	}
}

func TestInstallScriptQuotesCustomPrefixInShellConfig(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fake-bin")
	prefix := filepath.Join(home, "custom apps", "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGo(t, filepath.Join(fakeBin, "go"))

	cmd := exec.Command("sh", "./install.sh", "--prefix", prefix)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"SHELL=/bin/zsh",
		"ZDOTDIR=",
		"PATH="+fakeBin+":/usr/bin:/bin:/usr/sbin:/sbin",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}

	config, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	want := "export PATH='" + prefix + "':\"$PATH\""
	if !strings.Contains(string(config), want) {
		t.Fatalf("shell config lacks safely quoted custom prefix %q:\n%s", want, config)
	}
}

func writeFakeGo(t *testing.T, path string) {
	t.Helper()
	const script = `#!/bin/sh
set -eu
if [ "${1-}" = "version" ]; then
  echo "go version go1.26.0 test/arch"
  exit 0
fi
if [ "${1-}" != "build" ]; then
  echo "unexpected go command: $*" >&2
  exit 2
fi
shift
out=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    out=$2
    shift 2
  else
    shift
  fi
done
test -n "$out"
printf '#!/bin/sh\nexit 0\n' > "$out"
chmod 755 "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
