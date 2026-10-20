package engine

import (
	"reflect"
	"testing"
)

func TestWindowsBrowserCandidatesOrderAndSkipsEmptyRoots(t *testing.T) {
	env := map[string]string{
		"ProgramFiles":      `C:\Program Files`,
		"ProgramFiles(x86)": `C:\Program Files (x86)`,
		"LocalAppData":      "",
	}
	getenv := func(k string) string { return env[k] }

	got := windowsBrowserCandidates(getenv)
	want := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windowsBrowserCandidates() = %v, want %v", got, want)
	}
}

func TestBrowserPathPrefersAMTUIChromeOverride(t *testing.T) {
	t.Setenv("AMTUI_CHROME", `C:\custom\chrome.exe`)
	if got := browserPath(); got != `C:\custom\chrome.exe` {
		t.Fatalf("browserPath() = %q, want AMTUI_CHROME override", got)
	}
}
