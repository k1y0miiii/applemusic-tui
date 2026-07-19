package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	startMarker = "# >>> amtui installer >>>"
	endMarker   = "# <<< amtui installer <<<"
)

type installerTestEnv struct {
	root    string
	home    string
	fakeBin string
	tempDir string
	goLog   string
	env     []string
}

func TestInstallScriptInstallsAliasesAndConfiguresShellPath(t *testing.T) {
	tests := []struct {
		name       string
		shell      string
		configPath string
		pathLine   string
		guarded    bool
	}{
		{
			name:       "zsh",
			shell:      "/bin/zsh",
			configPath: ".zshrc",
			pathLine:   `export PATH="$HOME/.local/bin:$PATH"`,
			guarded:    true,
		},
		{
			name:       "fish",
			shell:      "/usr/bin/fish",
			configPath: ".config/fish/config.fish",
			pathLine:   `fish_add_path -g "$HOME/.local/bin"`,
		},
		{
			name:       "unknown shell",
			shell:      "/opt/local/bin/nushell",
			configPath: ".profile",
			pathLine:   `export PATH="$HOME/.local/bin:$PATH"`,
			guarded:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testEnv := newInstallerTestEnv(t, tt.shell, "Darwin")
			for range 2 {
				output, err := testEnv.run()
				if err != nil {
					t.Fatalf("install.sh failed: %v\n%s", err, output)
				}
			}

			assertInstalledCommands(t, filepath.Join(testEnv.home, ".local", "bin"))
			configPath := filepath.Join(testEnv.home, tt.configPath)
			config := mustReadFile(t, configPath)
			assertManagedBlock(t, configPath, config)
			if !strings.Contains(string(config), tt.pathLine) {
				t.Fatalf("%s lacks %q:\n%s", configPath, tt.pathLine, config)
			}
			if tt.guarded && !strings.Contains(string(config), `case ":$PATH:" in`) {
				t.Fatalf("%s PATH block is not guarded:\n%s", configPath, config)
			}
		})
	}
}

func TestInstallScriptConfiguresBashInteractiveAndLoginShells(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/bash", "Darwin")
	for range 2 {
		output, err := testEnv.run()
		if err != nil {
			t.Fatalf("install.sh failed: %v\n%s", err, output)
		}
	}

	bashrc := filepath.Join(testEnv.home, ".bashrc")
	bashProfile := filepath.Join(testEnv.home, ".bash_profile")
	for _, configPath := range []string{bashrc, bashProfile} {
		config := mustReadFile(t, configPath)
		assertManagedBlock(t, configPath, config)
		if !strings.Contains(string(config), `case ":$PATH:" in`) {
			t.Fatalf("%s PATH block is not guarded:\n%s", configPath, config)
		}
	}

	wantEntry := filepath.Join(testEnv.home, ".local", "bin")
	sourcedPath := sourcePOSIXConfigs(t, testEnv.env, bashrc, bashProfile, bashrc, bashProfile)
	if count := pathEntryCount(sourcedPath, wantEntry); count != 1 {
		t.Fatalf("sourcing bash configs twice produced %d PATH entries, want 1: %s", count, sourcedPath)
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	t.Run("interactive non-login", func(t *testing.T) {
		cmd := exec.Command(bash, "--noprofile", "--rcfile", bashrc, "-i", "-c",
			`printf '__AMTUI_PATH__%s\n' "$PATH"`)
		cmd.Env = replaceEnv(testEnv.env, "PATH=/usr/bin:/bin", "PS1=")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("interactive bash failed: %v\n%s", err, output)
		}
		got := markedValue(t, string(output), "__AMTUI_PATH__")
		if count := pathEntryCount(got, wantEntry); count != 1 {
			t.Fatalf("interactive bash PATH has %d entries, want 1: %s\n%s", count, got, output)
		}
	})
	t.Run("login", func(t *testing.T) {
		cmd := exec.Command(bash, "--norc", "--login", "-c",
			`printf '__AMTUI_PATH__%s\n' "$PATH"`)
		cmd.Env = replaceEnv(testEnv.env, "PATH=/usr/bin:/bin")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("login bash failed: %v\n%s", err, output)
		}
		got := markedValue(t, string(output), "__AMTUI_PATH__")
		if count := pathEntryCount(got, wantEntry); count != 1 {
			t.Fatalf("login bash PATH has %d entries, want 1: %s\n%s", count, got, output)
		}
	})
}

func TestInstallScriptUsesExistingProfileForBashLogin(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/bash", "Linux")
	profilePath := filepath.Join(testEnv.home, ".profile")
	original := []byte("# existing login settings\nexport KEEP=1\n")
	if err := os.WriteFile(profilePath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := testEnv.run()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	profile := mustReadFile(t, profilePath)
	if !bytes.HasPrefix(profile, original) {
		t.Fatalf("existing .profile contents were not preserved:\n%s", profile)
	}
	assertManagedBlock(t, profilePath, profile)
	bashrcPath := filepath.Join(testEnv.home, ".bashrc")
	assertManagedBlock(t, bashrcPath, mustReadFile(t, bashrcPath))
	if _, statErr := os.Lstat(filepath.Join(testEnv.home, ".bash_profile")); !os.IsNotExist(statErr) {
		t.Fatalf("installer shadowed existing .profile with .bash_profile: %v", statErr)
	}

	wantEntry := filepath.Join(testEnv.home, ".local", "bin")
	sourcedPath := sourcePOSIXConfigs(t, testEnv.env, bashrcPath, profilePath)
	if count := pathEntryCount(sourcedPath, wantEntry); count != 1 {
		t.Fatalf("bash config fallback produced %d PATH entries, want 1: %s", count, sourcedPath)
	}
}

func TestInstallScriptPreservesSymlinkedShellConfig(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Darwin")
	targetDir := filepath.Join(testEnv.home, "shell config")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(targetDir, "real.zshrc")
	original := []byte("# existing settings\nexport KEEP=1\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(testEnv.home, ".zshrc")
	if err := os.Symlink(filepath.Join("shell config", "real.zshrc"), configPath); err != nil {
		t.Fatal(err)
	}

	output, err := testEnv.run()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}

	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink", configPath)
	}
	target := mustReadFile(t, targetPath)
	if !bytes.HasPrefix(target, original) {
		t.Fatalf("symlink target lost existing contents:\n%s", target)
	}
	assertManagedBlock(t, targetPath, target)
}

func TestInstallScriptRejectsUnterminatedManagedBlockWithoutChanges(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Darwin")
	configPath := filepath.Join(testEnv.home, ".zshrc")
	original := []byte("# keep this byte-for-byte\n" + startMarker + "\nexport BROKEN=1")
	if err := os.WriteFile(configPath, original, 0o640); err != nil {
		t.Fatal(err)
	}

	output, err := testEnv.run()
	if err == nil {
		t.Fatalf("install.sh accepted an unterminated managed block:\n%s", output)
	}
	if !strings.Contains(strings.ToLower(string(output)), "unterminated") {
		t.Fatalf("error does not explain the unterminated block:\n%s", output)
	}
	if got := mustReadFile(t, configPath); !bytes.Equal(got, original) {
		t.Fatalf("malformed config changed\nwant: %q\n got: %q", original, got)
	}
	if strings.Contains(readFileIfExists(t, testEnv.goLog), "build") {
		t.Fatalf("build ran before shell config preflight:\n%s", readFileIfExists(t, testEnv.goLog))
	}
	if _, statErr := os.Stat(filepath.Join(testEnv.home, ".local", "bin")); !os.IsNotExist(statErr) {
		t.Fatalf("failed preflight created install directory: %v", statErr)
	}
}

func TestInstallScriptPreflightsEveryDestinationConflict(t *testing.T) {
	tests := []struct {
		name     string
		conflict string
		symlink  bool
	}{
		{name: "main binary symlink", conflict: "amtui", symlink: true},
		{name: "applemusic regular file", conflict: "applemusic"},
		{name: "applemusic-tui regular file", conflict: "applemusic-tui"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
			prefix := filepath.Join(testEnv.root, "prefix")
			if err := os.MkdirAll(prefix, 0o755); err != nil {
				t.Fatal(err)
			}
			conflictPath := filepath.Join(prefix, tt.conflict)
			if tt.symlink {
				victim := filepath.Join(testEnv.root, "victim")
				if err := os.WriteFile(victim, []byte("do not overwrite"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(victim, conflictPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(conflictPath, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}

			output, err := testEnv.run("--prefix", prefix, "--no-path")
			if err == nil {
				t.Fatalf("install.sh accepted destination conflict:\n%s", output)
			}
			if !strings.Contains(string(output), "already exists") {
				t.Fatalf("conflict error is unclear:\n%s", output)
			}
			if strings.Contains(readFileIfExists(t, testEnv.goLog), "build") {
				t.Fatalf("build ran before conflict preflight:\n%s", readFileIfExists(t, testEnv.goLog))
			}

			for _, name := range []string{"amtui", "applemusic", "applemusic-tui"} {
				path := filepath.Join(prefix, name)
				if name == tt.conflict {
					continue
				}
				if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
					t.Fatalf("conflict left partial destination %s: %v", path, statErr)
				}
			}
			if tt.symlink {
				if _, readlinkErr := os.Readlink(conflictPath); readlinkErr != nil {
					t.Fatalf("main conflict symlink was replaced: %v", readlinkErr)
				}
			} else if got := mustReadFile(t, conflictPath); string(got) != "keep" {
				t.Fatalf("alias conflict changed: %q", got)
			}
		})
	}
}

func TestInstallScriptPreflightsInvalidPrefixBeforeBuild(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	prefix := filepath.Join(testEnv.root, "prefix-is-a-file")
	if err := os.WriteFile(prefix, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := testEnv.run("--prefix", prefix, "--no-path")
	if err == nil {
		t.Fatalf("install.sh accepted a non-directory prefix:\n%s", output)
	}
	if !strings.Contains(string(output), "already exists") {
		t.Fatalf("invalid-prefix error is unclear:\n%s", output)
	}
	if strings.Contains(readFileIfExists(t, testEnv.goLog), "build") {
		t.Fatalf("build ran before prefix preflight:\n%s", readFileIfExists(t, testEnv.goLog))
	}
	if got := mustReadFile(t, prefix); string(got) != "keep" {
		t.Fatalf("invalid prefix changed: %q", got)
	}
}

func TestInstallScriptReplacesAliasSymlinkWithoutFollowingDirectory(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	prefix := filepath.Join(testEnv.root, "prefix")
	aliasTargetDir := filepath.Join(testEnv.root, "must-stay-empty")
	for _, dir := range []string{prefix, aliasTargetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	aliasPath := filepath.Join(prefix, "applemusic")
	if err := os.Symlink(aliasTargetDir, aliasPath); err != nil {
		t.Fatal(err)
	}

	output, err := testEnv.run("--prefix", prefix, "--no-path")
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	target, err := os.Readlink(aliasPath)
	if err != nil {
		t.Fatalf("applemusic alias was not preserved as a symlink: %v", err)
	}
	if target != "amtui" {
		t.Fatalf("applemusic -> %q, want amtui", target)
	}
	entries, err := os.ReadDir(aliasTargetDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("installer followed alias symlink into target directory: %v", entries)
	}
}

func TestInstallScriptCleansShellTempFileOnFailure(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	writeExecutable(t, filepath.Join(testEnv.fakeBin, "mv"), `#!/bin/sh
set -eu
for arg in "$@"; do
  case "$arg" in
    */.amtui-shell.*|*/amtui-shell.*) exit 23 ;;
  esac
done
exec /bin/mv "$@"
`)

	output, err := testEnv.run()
	if err == nil {
		t.Fatalf("install.sh unexpectedly survived shell-config mv failure:\n%s", output)
	}
	for _, pattern := range []string{
		filepath.Join(testEnv.home, ".amtui-shell.*"),
		filepath.Join(testEnv.tempDir, ".amtui-shell.*"),
		filepath.Join(testEnv.tempDir, "amtui-shell.*"),
	} {
		matches, globErr := filepath.Glob(pattern)
		if globErr != nil {
			t.Fatal(globErr)
		}
		if len(matches) != 0 {
			t.Fatalf("shell temp file leaked after failure: %v", matches)
		}
	}
}

func TestInstallScriptPathGuardHandlesQuotedPrefixAndRepeatedSource(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	prefix := filepath.Join(testEnv.root, "custom apps", "it's", "bin")
	output, err := testEnv.run("--prefix", prefix)
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}

	configPath := filepath.Join(testEnv.home, ".zshrc")
	sourcedPath := sourcePOSIXConfigs(t, testEnv.env, configPath, configPath, configPath)
	if count := pathEntryCount(sourcedPath, prefix); count != 1 {
		t.Fatalf("repeated source produced %d custom-prefix entries, want 1: %s", count, sourcedPath)
	}
}

func TestInstallScriptUsesZDOTDIRAndPrintsSafeSourceInstruction(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	zdotdir := filepath.Join(testEnv.root, `z dot's "$HOME" directory`)
	testEnv.env = replaceEnv(testEnv.env, "ZDOTDIR="+zdotdir)

	output, err := testEnv.run()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	configPath := filepath.Join(zdotdir, ".zshrc")
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("ZDOTDIR config: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(testEnv.home, ".zshrc")); !os.IsNotExist(statErr) {
		t.Fatalf("installer ignored nonempty ZDOTDIR: %v", statErr)
	}

	instruction := sourceInstruction(t, string(output))
	cmd := exec.Command("sh", "-c", instruction+`; printf '__AMTUI_PATH__%s\n' "$PATH"`)
	cmd.Env = replaceEnv(testEnv.env, "PATH=/usr/bin:/bin")
	sourceOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("printed source instruction is unsafe or ambiguous: %q\n%v\n%s",
			instruction, err, sourceOutput)
	}
	gotPath := markedValue(t, string(sourceOutput), "__AMTUI_PATH__")
	wantEntry := filepath.Join(testEnv.home, ".local", "bin")
	if count := pathEntryCount(gotPath, wantEntry); count != 1 {
		t.Fatalf("source instruction produced %d PATH entries, want 1: %s", count, gotPath)
	}
}

func TestInstallScriptHelpWorksWithoutHome(t *testing.T) {
	cmd := exec.Command("sh", "./install.sh", "--help")
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "LC_ALL=C"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--help requires HOME: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Usage:") || !strings.Contains(string(output), "--prefix") {
		t.Fatalf("unexpected help output:\n%s", output)
	}
}

func TestInstallScriptSetsPlatformBuildEnvironment(t *testing.T) {
	tests := []struct {
		osName string
		cgo    string
		macOS  string
		cache  bool
	}{
		{osName: "Darwin", cgo: "1", macOS: "14.2", cache: true},
		{osName: "Linux", cgo: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.osName, func(t *testing.T) {
			testEnv := newInstallerTestEnv(t, "/bin/zsh", tt.osName)
			prefix := filepath.Join(testEnv.root, "prefix")
			output, err := testEnv.run("--prefix", prefix, "--no-path")
			if err != nil {
				t.Fatalf("install.sh failed: %v\n%s", err, output)
			}
			log := readFileIfExists(t, testEnv.goLog)
			if !strings.Contains(log, "CGO_ENABLED="+tt.cgo+"\n") {
				t.Fatalf("%s build has wrong CGO environment:\n%s", tt.osName, log)
			}
			if !strings.Contains(log, "MACOSX_DEPLOYMENT_TARGET="+tt.macOS+"\n") {
				t.Fatalf("%s build has wrong deployment target:\n%s", tt.osName, log)
			}
			cache := logValue(log, "GOCACHE")
			if tt.cache && cache == "" {
				t.Fatalf("Darwin build did not isolate GOCACHE:\n%s", log)
			}
			if !tt.cache && cache != "" {
				t.Fatalf("Linux build unexpectedly set GOCACHE=%q", cache)
			}
		})
	}
}

func TestInstallScriptTreatsGoVersionFailureAsFatal(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	testEnv.env = replaceEnv(testEnv.env, "FAKE_GO_VERSION_FAIL=1")
	prefix := filepath.Join(testEnv.root, "prefix")

	output, err := testEnv.run("--prefix", prefix, "--no-path")
	if err == nil {
		t.Fatalf("install.sh ignored go version failure:\n%s", output)
	}
	if !strings.Contains(strings.ToLower(string(output)), "go version") {
		t.Fatalf("go version failure is unclear:\n%s", output)
	}
	log := readFileIfExists(t, testEnv.goLog)
	if strings.Contains(log, "build") {
		t.Fatalf("build ran after go version failed:\n%s", log)
	}
	if _, statErr := os.Stat(prefix); !os.IsNotExist(statErr) {
		t.Fatalf("go version failure created install prefix: %v", statErr)
	}
}

func TestInstallScriptFallsBackFromReadOnlyGoModuleCacheAndCleansIt(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	moduleCache := filepath.Join(testEnv.root, "readonly-module-cache")
	if err := os.MkdirAll(moduleCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(moduleCache, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(moduleCache, 0o755)
	})
	testEnv.env = replaceEnv(testEnv.env,
		"GOMODCACHE="+moduleCache,
		"FAKE_GO_READONLY_CACHE=1",
	)

	output, err := testEnv.run("--prefix", filepath.Join(testEnv.root, "prefix"), "--no-path")
	if err != nil {
		t.Fatalf("install.sh failed instead of using a temporary module cache: %v\n%s", err, output)
	}
	lowerOutput := strings.ToLower(string(output))
	if !strings.Contains(lowerOutput, "not writable") ||
		!strings.Contains(lowerOutput, "temporary module cache") {
		t.Fatalf("module-cache fallback was not reported:\n%s", output)
	}
	log := readFileIfExists(t, testEnv.goLog)
	usedCache := logValue(log, "GOMODCACHE")
	if usedCache == "" || usedCache == moduleCache {
		t.Fatalf("build did not use a fallback GOMODCACHE:\n%s", log)
	}
	if _, statErr := os.Stat(usedCache); !os.IsNotExist(statErr) {
		t.Fatalf("temporary module cache was not cleaned: %s (%v)", usedCache, statErr)
	}
	info, err := os.Stat(moduleCache)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o555 {
		t.Fatalf("installer chmodded configured module cache: %v", got)
	}
}

func TestInstallScriptStagesMainBinaryBeforeAtomicMove(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	fsLog := filepath.Join(testEnv.root, "fs.log")
	testEnv.env = replaceEnv(testEnv.env, "FAKE_FS_LOG="+fsLog)
	writeExecutable(t, filepath.Join(testEnv.fakeBin, "install"), `#!/bin/sh
set -eu
printf 'install-destination=%s\n' "$4" >> "$FAKE_FS_LOG"
/bin/cp "$3" "$4"
/bin/chmod 755 "$4"
`)
	writeExecutable(t, filepath.Join(testEnv.fakeBin, "mv"), `#!/bin/sh
set -eu
previous=
last=
for arg in "$@"; do
  previous=$last
  last=$arg
done
printf 'mv-source=%s\nmv-destination=%s\n' "$previous" "$last" >> "$FAKE_FS_LOG"
exec /bin/mv "$@"
`)
	prefix := filepath.Join(testEnv.root, "prefix")

	output, err := testEnv.run("--prefix", prefix, "--no-path")
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	log := readFileIfExists(t, fsLog)
	finalPath := filepath.Join(prefix, "amtui")
	if strings.Contains(log, "install-destination="+finalPath+"\n") {
		t.Fatalf("install wrote directly to final destination:\n%s", log)
	}
	if !strings.Contains(log, "install-destination="+filepath.Join(prefix, ".amtui-install.")) {
		t.Fatalf("install did not stage inside the destination directory:\n%s", log)
	}
	if !strings.Contains(log, "mv-destination="+finalPath+"\n") {
		t.Fatalf("staged binary was not atomically moved into place:\n%s", log)
	}
	assertInstalledCommands(t, prefix)
}

func TestInstallScriptSupportsCustomPrefixWithoutPathMutation(t *testing.T) {
	testEnv := newInstallerTestEnv(t, "/bin/zsh", "Linux")
	prefix := filepath.Join(testEnv.root, "custom", "bin")
	output, err := testEnv.run("--prefix", prefix, "--no-path")
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	assertInstalledCommands(t, prefix)
	if _, statErr := os.Stat(filepath.Join(testEnv.home, ".zshrc")); !os.IsNotExist(statErr) {
		t.Fatalf("--no-path created shell config: %v", statErr)
	}
}

func newInstallerTestEnv(t *testing.T, shell, osName string) *installerTestEnv {
	t.Helper()
	root := t.TempDir()
	testEnv := &installerTestEnv{
		root:    root,
		home:    filepath.Join(root, "home"),
		fakeBin: filepath.Join(root, "fake-bin"),
		tempDir: filepath.Join(root, "tmp"),
		goLog:   filepath.Join(root, "go.log"),
	}
	for _, dir := range []string{testEnv.home, testEnv.fakeBin, testEnv.tempDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeGo(t, filepath.Join(testEnv.fakeBin, "go"))
	writeExecutable(t, filepath.Join(testEnv.fakeBin, "uname"), `#!/bin/sh
set -eu
printf '%s\n' "${FAKE_UNAME:?}"
`)
	writeExecutable(t, filepath.Join(testEnv.fakeBin, "clang"), "#!/bin/sh\nexit 0\n")
	testEnv.env = []string{
		"HOME=" + testEnv.home,
		"SHELL=" + shell,
		"ZDOTDIR=",
		"TMPDIR=" + testEnv.tempDir,
		"PATH=" + testEnv.fakeBin + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"FAKE_UNAME=" + osName,
		"FAKE_GO_LOG=" + testEnv.goLog,
		"LANG=C",
		"LC_ALL=C",
	}
	return testEnv
}

func (testEnv *installerTestEnv) run(args ...string) ([]byte, error) {
	commandArgs := append([]string{"./install.sh"}, args...)
	cmd := exec.Command("sh", commandArgs...)
	cmd.Env = testEnv.env
	return cmd.CombinedOutput()
}

func writeFakeGo(t *testing.T, path string) {
	t.Helper()
	const script = `#!/bin/sh
set -eu
: "${FAKE_GO_LOG:?}"
case "${1-}" in
  version)
    printf 'version\n' >> "$FAKE_GO_LOG"
    if [ "${FAKE_GO_VERSION_FAIL-0}" = 1 ]; then
      printf 'fake go version failed\n' >&2
      exit 9
    fi
    printf 'go version go1.26.0 test/arch\n'
    ;;
  env)
    if [ "${2-}" != GOMODCACHE ]; then
      printf 'unexpected go env query: %s\n' "${2-}" >&2
      exit 2
    fi
    printf 'env GOMODCACHE\n' >> "$FAKE_GO_LOG"
    printf '%s\n' "${FAKE_DEFAULT_GOMODCACHE-}"
    ;;
  build)
    printf 'build\nCGO_ENABLED=%s\nMACOSX_DEPLOYMENT_TARGET=%s\nGOCACHE=%s\nGOMODCACHE=%s\n' \
      "${CGO_ENABLED-}" "${MACOSX_DEPLOYMENT_TARGET-}" "${GOCACHE-}" "${GOMODCACHE-}" \
      >> "$FAKE_GO_LOG"
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
    if [ "${FAKE_GO_READONLY_CACHE-0}" = 1 ]; then
      test -n "${GOMODCACHE-}"
      mkdir -p "$GOMODCACHE/locked"
      printf 'readonly module data\n' > "$GOMODCACHE/locked/module"
      chmod 400 "$GOMODCACHE/locked/module"
      chmod 500 "$GOMODCACHE/locked" "$GOMODCACHE"
    fi
    printf '#!/bin/sh\nexit 0\n' > "$out"
    chmod 755 "$out"
    ;;
  *)
    printf 'unexpected go command: %s\n' "$*" >&2
    exit 2
    ;;
esac
`
	writeExecutable(t, path, script)
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertInstalledCommands(t *testing.T, installDir string) {
	t.Helper()
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
}

func assertManagedBlock(t *testing.T, path string, contents []byte) {
	t.Helper()
	text := string(contents)
	if count := strings.Count(text, startMarker); count != 1 {
		t.Fatalf("%s start marker count = %d, want 1\n%s", path, count, text)
	}
	if count := strings.Count(text, endMarker); count != 1 {
		t.Fatalf("%s end marker count = %d, want 1\n%s", path, count, text)
	}
}

func sourcePOSIXConfigs(t *testing.T, env []string, paths ...string) string {
	t.Helper()
	var script strings.Builder
	script.WriteString("PATH=/usr/bin:/bin\n")
	for _, path := range paths {
		script.WriteString(". ")
		script.WriteString(shellQuote(path))
		script.WriteByte('\n')
	}
	script.WriteString(`printf '__AMTUI_PATH__%s\n' "$PATH"`)
	cmd := exec.Command("sh", "-c", script.String())
	cmd.Env = replaceEnv(env, "PATH=/usr/bin:/bin")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing shell configs failed: %v\n%s\nscript:\n%s", err, output, script.String())
	}
	return markedValue(t, string(output), "__AMTUI_PATH__")
}

func sourceInstruction(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ". ") {
			return trimmed
		}
	}
	t.Fatalf("installer output has no source instruction:\n%s", output)
	return ""
}

func markedValue(t *testing.T, output, marker string) string {
	t.Helper()
	index := strings.LastIndex(output, marker)
	if index < 0 {
		t.Fatalf("output lacks marker %q:\n%s", marker, output)
	}
	value := output[index+len(marker):]
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		value = value[:newline]
	}
	return value
}

func pathEntryCount(pathValue, entry string) int {
	count := 0
	for _, candidate := range strings.Split(pathValue, ":") {
		if candidate == entry {
			count++
		}
	}
	return count
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func replaceEnv(env []string, replacements ...string) []string {
	keys := make(map[string]struct{}, len(replacements))
	for _, replacement := range replacements {
		key, _, _ := strings.Cut(replacement, "=")
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(env)+len(replacements))
	for _, variable := range env {
		key, _, _ := strings.Cut(variable, "=")
		if _, replaced := keys[key]; !replaced {
			result = append(result, variable)
		}
	}
	return append(result, replacements...)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func readFileIfExists(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func logValue(log, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
