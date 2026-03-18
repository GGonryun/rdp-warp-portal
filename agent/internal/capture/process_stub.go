//go:build !windows

package capture

import (
	"fmt"
	"os"
)

// launchInSession is not supported on non-Windows platforms.
func launchInSession(sessionID uint32, exePath string, args []string, stderrPath string) (int, *os.File, error) {
	return 0, nil, fmt.Errorf("launchInSession is only supported on Windows")
}
