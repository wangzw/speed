//go:build race

package treecheck

// raceEnabled says this test binary is built with the race detector, and so
// builds the program under test with it too.
const raceEnabled = true
