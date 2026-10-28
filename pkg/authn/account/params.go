package account

import (
	"errors"
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

// signup is the registration switch: whether a first login through a social
// provider or an SMS code makes an account. A read that fails reads as closed.
func (p *product) signup() string {
	return readSetting(p, signupPath, signupInDomain, defaults.Signup)
}

// passwordSignup is the self-registration switch. Signups.Register reads it on
// every call, and it is the authority there; a read that fails reads as closed,
// which is the strict value.
func (p *product) passwordSignup() string {
	return readSetting(p, passwordSignupPath, passwordSignupInDomain, defaults.PasswordSignup)
}

// unverifiedTTL is how long a self-registered account that proved no email
// address may stay. Zero turns the sweep off.
func (p *product) unverifiedTTL() time.Duration {
	return readSetting(p, unverifiedTTLPath, unverifiedTTLInDomain, defaults.UnverifiedTTL)
}

// unverifiedSweepInterval is how often the sweep of those accounts runs. It is
// read before every pass, so a change takes effect without a restart.
func (p *product) unverifiedSweepInterval() time.Duration {
	return readSetting(p, unverifiedSweepPath, unverifiedSweepInDomain, defaults.UnverifiedSweepInterval)
}

// minLength is the fewest characters a new password has.
func (p *product) minLength() int {
	return readSetting(p, minLengthPath, minLengthInDomain, defaults.PasswordMinLength)
}

// passwordAlgorithm is the algorithm a new password hash is written with, read
// from the settings store. The store answers the declared default when no layer
// holds a row, so an absent row does not reach this function as an error. A read
// fault, or a value outside the domain, is not replaced by another algorithm: the
// read returns the fault, and the write that asked for it fails instead. A fault
// is reported at Error once. ErrUnknownKey is the store's answer for a key no
// declaration covers, which is a defect in this module's own schema: the declared
// algorithm is used so the write can proceed, and the defect is reported rather
// than hidden.
func (p *product) passwordAlgorithm() (password.Algorithm, error) {
	fallback := password.Algorithm(defaults.PasswordAlgorithm)
	if p.store == nil {
		return fallback, nil
	}
	value, err := settings.Get[string](p.store, settings.Global(), algorithmPath)
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		p.report(algorithmPath, "the setting is not declared, so its declared default is used", err.Error())
		return fallback, nil
	case err != nil:
		p.report(algorithmPath, "the setting cannot be read, so a password cannot be written", err.Error())
		return "", err
	case !algorithmInDomain(value):
		p.report(algorithmPath, "the setting holds a value outside its domain, so a password cannot be written", value)
		return "", fmt.Errorf("the password algorithm setting %s is outside its domain", algorithmPath)
	}
	p.report(algorithmPath, "", "")
	return password.Algorithm(value), nil
}

// hashParams is what a new password hash is written with: the algorithm the
// settings state and the cost the settings state for it. The three Argon2id
// settings are each judged in their own range, and the OWASP floor is judged on
// the three together, so a combination that is below it, or that an entry cannot
// state, is the default combination instead, and says so at Error. The algorithm
// is never substituted: an unreadable one is a fault the caller does not write
// through.
func (p *product) hashParams() (password.Params, error) {
	algorithm, err := p.passwordAlgorithm()
	if err != nil {
		return password.Params{}, err
	}
	var want password.Params
	if algorithm == password.PBKDF2SHA256 {
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
	return effective, nil
}

// decoyParams is what a decoy entry is made with: the current write parameters
// when they can be read, and the declared combination when the algorithm cannot
// be. A decoy is never stored, so the fault the read already reported is not a
// refusal here: the check still costs one derivation.
func (p *product) decoyParams() password.Params {
	params, err := p.hashParams()
	if err != nil {
		return password.DefaultParams()
	}
	return params
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
