package password

import (
	"sync"
	"testing"
)

// TestTheDecoyIsSafeUnderConcurrentLogins: the decoy is made once under concurrent logins, for the parameters asked.
func TestTheDecoyIsSafeUnderConcurrentLogins(t *testing.T) {
	var d Decoy
	all := []Params{cheapArgon2id, {Algorithm: Argon2id, MemoryKiB: 128, Iterations: 1, Parallelism: 1}, cheapPBKDF2}
	var wg sync.WaitGroup
	for g := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				p := all[(g+i)%len(all)]
				e, err := d.For(p)
				if err != nil {
					t.Errorf("making a decoy: %v", err)
					return
				}
				if e.Params() != p {
					t.Errorf("the decoy for %+v has %+v", p, e.Params())
					return
				}
			}
		}()
	}
	wg.Wait()
}
