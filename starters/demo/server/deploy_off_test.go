//go:build !deploy

package main

import "testing"

func TestTheDefaultAssemblyCarriesNoMailPath(t *testing.T) {
	if len(deployModules) != 0 {
		t.Errorf("the default build names %v; want no deployment modules", deployModules)
	}
}
