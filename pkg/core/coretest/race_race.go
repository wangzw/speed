//go:build race

package coretest

// raceFactor lengthens a bound for a binary built with the race detector, whose
// program under test runs several times slower.
const raceFactor = 3
