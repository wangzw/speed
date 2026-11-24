package account

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/settings"
)

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

func (p *product) report(key, problem, detail string) {
	p.reportOnce(slog.LevelError, key, key, problem, detail)
}

func (p *product) reportOnce(level slog.Level, key, setting, problem, detail string) {
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
	if problem == "" {
		return
	}
	if level >= slog.LevelError {
		p.logger.Error(ModuleName+": "+problem, slog.String("setting", setting))
		return
	}
	p.logger.Warn(ModuleName+": "+problem, slog.String("setting", setting))
}

func (p *product) signup() string {
	return readSetting(p, signupPath, signupInDomain, defaults.Signup)
}

func (p *product) passwordSignup() string {
	return readSetting(p, passwordSignupPath, passwordSignupInDomain, defaults.PasswordSignup)
}

func (p *product) unverifiedTTL() time.Duration {
	return readSetting(p, unverifiedTTLPath, unverifiedTTLInDomain, defaults.UnverifiedTTL)
}

func (p *product) unverifiedSweepInterval() time.Duration {
	return readSetting(p, unverifiedSweepPath, unverifiedSweepInDomain, defaults.UnverifiedSweepInterval)
}

func (p *product) minLength() int {
	return readSetting(p, minLengthPath, minLengthInDomain, defaults.PasswordMinLength)
}

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

func (p *product) decoyParams() password.Params {
	params, err := p.hashParams()
	if err != nil {
		return password.DefaultParams()
	}
	return params
}

type throttleParams struct {
	free     int
	maxDelay time.Duration
	decay    time.Duration
}

func (p *product) throttleParams() throttleParams {
	return throttleParams{
		free:     readSetting(p, freeAttemptsPath, freeAttemptsInDomain, defaults.ThrottleFreeAttempts),
		maxDelay: readSetting(p, maxDelayPath, maxDelayInDomain, defaults.ThrottleMaxDelay),
		decay:    readSetting(p, decayPath, decayInDomain, defaults.ThrottleDecay),
	}
}

func (t throttleParams) delay(n int) time.Duration {
	over := n - t.free
	if over <= 0 {
		return 0
	}
	// 2^over seconds overflows a Duration past 33 doublings, beyond any cap the
	// setting allows.
	if over > 30 {
		return t.maxDelay
	}
	return min(time.Duration(1<<over)*time.Second, t.maxDelay)
}

func (p *product) retention() time.Duration {
	return readSetting(p, retentionPath, retentionInDomain, defaults.SecretRetention)
}

func asUint32(n, max int) uint32 {
	if n < 0 {
		n = 0
	}
	if n > max {
		n = max
	}
	return uint32(n)
}

func asUint8(n, max int) uint8 {
	if n < 0 {
		n = 0
	}
	if n > max {
		n = max
	}
	return uint8(n)
}
