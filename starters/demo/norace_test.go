//go:build !race

package main

// raceEnabled says this test binary is built with the race detector, and so
// builds the program under test with it too.
const raceEnabled = false
