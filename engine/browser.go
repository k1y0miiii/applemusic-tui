package engine

import (
	"os"
	"runtime"
)

// browserPath returns an explicit browser binary to pass chromedp, or "" to
// leave chromedp's own lookup in charge. chromedp's Windows search only knows
// about Chrome at a handful of fixed C:\Program Files paths and falls back to
// "google-chrome" on a miss — it never looks for Microsoft Edge, which every
// Windows 10/11 box ships with, and misses Chrome installed on another drive
// or under a non-default %ProgramFiles%.
func browserPath() string {
	if p := os.Getenv("AMTUI_CHROME"); p != "" {
		return p
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, candidate := range windowsBrowserCandidates(os.Getenv) {
		if fi, err := os.Stat(candidate); err == nil && fi.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

// windowsBrowserCandidates lists Chrome (preferred) then Edge install paths
// across every root Windows commonly installs browsers under. Built with
// plain string concatenation rather than filepath.Join so this stays testable
// on non-Windows hosts.
func windowsBrowserCandidates(getenv func(string) string) []string {
	var roots []string
	for _, envVar := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if v := getenv(envVar); v != "" {
			roots = append(roots, v)
		}
	}
	suffixes := []string{
		`\Google\Chrome\Application\chrome.exe`,
		`\Microsoft\Edge\Application\msedge.exe`,
	}
	var candidates []string
	for _, suffix := range suffixes {
		for _, root := range roots {
			candidates = append(candidates, root+suffix)
		}
	}
	return candidates
}
