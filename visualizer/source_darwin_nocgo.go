//go:build darwin && !cgo

package visualizer

import "fmt"

func openSystemSource() (Source, error) {
	return nil, fmt.Errorf(
		"%w: macOS CoreAudio system capture requires a cgo-enabled build",
		ErrUnavailable,
	)
}
