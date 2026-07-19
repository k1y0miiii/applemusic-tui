//go:build !darwin && !linux

package visualizer

import (
	"fmt"
	"runtime"
)

func openSystemSource() (Source, error) {
	return nil, fmt.Errorf("%s is not supported for system audio capture", runtime.GOOS)
}
