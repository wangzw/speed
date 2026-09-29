//go:build !linux

package main

import "os/exec"

// configureChild has nothing to set where the kernel offers no signal on the
// parent's death. A child that outlives a killed test binary there is found by
// the next run's reapOrphans instead, and a timeout is headed off by the
// watchdog each launch arms.
func configureChild(*exec.Cmd) {}
