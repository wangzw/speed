package account

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/settings"
)

// readSetting reads one setting from the store under the type it is declared
// with, and is the declared default when the assembly has no store, when the
// read fails and when the value is outside the domain, which the store judges when
// a value is written and not when it is read. A setting is read on every
// request, so a fault is reported at Error once, and again only when it changes or
// has been cleared in between.
func readSetting[T any](p *product, path string, inDomain func(T) bool, fallback T) T {
	if p.store == nil {
		return fallback
	}
	value, err := settings.Get[T](p.store, settings.Global(), path)
	switch {
	case err != nil:
		p.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(value):
		p.report(path, "the setting holds a value outside its domain, so its declared default is used", fmt.Sprint(value))
		return fallback
	}
	p.report(path, "", "")
	return value
}

// report writes the Error line for a fault of one setting or one combination of
// them. The same fault is written once; an empty problem clears it so that it is
// written again if it comes back.
func (p *product) report(key, problem, detail string) {
	p.mu.Lock()
	last, reported := p.reported[key]
	switch {
	case problem == "":
		delete(p.reported, key)
	case !reported || last != detail:
		p.reported[key] = detail
	default:
		problem = ""
	}
	p.mu.Unlock()
	if problem != "" {
		p.logger.Error(ModuleName+": "+problem, slog.String("setting", key))
	}
}

// minLength is the fewest characters a new password has.
func (p *product) minLength() int {
	return readSetting(p, minLengthPath, minLengthInDomain, defaults.PasswordMinLength)
}

// hashParams is what a new password hash is written with: the algorithm the
// configuration states and the cost the settings state for it. The three
// Argon2id settings are each judged in their own range, and the OWASP floor is
// judged on the three together, so a combination that is below it, or that an
// entry cannot state, is the default combination instead, and says so at Error.
func (p *product) hashParams() password.Params {
	var want password.Params
	if p.cfg.algorithm == password.PBKDF2SHA256 {
		want = password.Params{
			Algorithm:        password.PBKDF2SHA256,
			PBKDF2Iterations: readSetting(p, pbkdf2IterPath, pbkdf2IterationsInDomain, defaults.PasswordPBKDF2Iterations),
		}
	} else {
		memory := readSetting(p, argonMemoryPath, argonMemoryInDomain, defaults.PasswordArgon2Memory)
		iterations := readSetting(p, argonIterationPath, argonIterationsInDomain, defaults.PasswordArgon2Iterations)
		lanes := readSetting(p, argonLanesPath, argonLanesInDomain, defaults.PasswordArgon2Lanes)
		want = password.Params{
			Algorithm:   password.Argon2id,
			MemoryKiB:   asUint32(memory, password.MaxMemoryKiB),
			Iterations:  asUint32(iterations, password.MaxIterations),
			Parallelism: asUint8(lanes, password.MaxParallelism),
		}
	}
	effective, fellBack := want.Effective()
	if fellBack {
		p.report("password-params", "the password cost settings combine below what OWASP recommends, "+
			"so the default combination is used", fmt.Sprintf("%+v", want))
	} else {
		p.report("password-params", "", "")
	}
	return effective
}

// throttleParams are the three numbers of the back-off, each as the setting says
// or as it is declared.
type throttleParams struct {
	free     int
	maxDelay time.Duration
	decay    time.Duration
}

// throttleParams reads the back-off settings.
func (p *product) throttleParams() throttleParams {
	return throttleParams{
		free:     readSetting(p, freeAttemptsPath, freeAttemptsInDomain, defaults.ThrottleFreeAttempts),
		maxDelay: readSetting(p, maxDelayPath, maxDelayInDomain, defaults.ThrottleMaxDelay),
		decay:    readSetting(p, decayPath, decayInDomain, defaults.ThrottleDecay),
	}
}

// delay is how long a key that has failed n times is made to wait: no time for the
// free attempts, and after them two to the power of the failures beyond them, in
// seconds, up to the longest wait.
func (t throttleParams) delay(n int) time.Duration {
	over := n - t.free
	if over <= 0 {
		return 0
	}
	// 2^over seconds overflows a Duration past 33 doublings, long after the cap
	// has been passed for any cap the setting allows.
	if over > 30 {
		return t.maxDelay
	}
	return min(time.Duration(1<<over)*time.Second, t.maxDelay)
}

// retention is how long the row of a service secret is kept once it expired or was
// revoked.
func (p *product) retention() time.Duration {
	return readSetting(p, retentionPath, retentionInDomain, defaults.SecretRetention)
}

// asUint32 is n as the width a derivation parameter states, within max. A value
// outside the domain comes only from a store written around it, and reads as the
// bound so that a parameter never wraps.
func asUint32(n, max int) uint32 {
	if n < 0 {
		n = 0
	}
	if n > max {
		n = max
	}
	return uint32(n)
}

// asUint8 is asUint32 in the width an Argon2id lane count takes.
func asUint8(n, max int) uint8 {
	if n < 0 {
		n = 0
	}
	if n > max {
		n = max
	}
	return uint8(n)
}
