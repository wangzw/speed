//go:build race

package main

// raceEnabled says this test binary is built with the race detector, and so
// builds the host with it too.
const raceEnabled = true
