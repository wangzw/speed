# pkg/authn/login/password

The password login method, registered as `authn.login.password`. It is one
`authn.LoginMethod` among the first factors: it checks an identifier and a
password, and answers the engine a `Step`. It delivers the login-method
capability non-exclusively, so it stands beside every other method, and takes
no first-factor capability — a host writes this module's name into a command
beside the login engine and an account module.

The method is this package's whole surface. It touches no HTTP, session, token
or flow record and writes no log line; it reads the identifier's back-off before
it derives anything, hands the credential to the account module's `Passwords`,
counts the outcome against the same key, and says what is next. The engine, not
this package, logs the cause of an answer.

The API is the package's godoc, and the `LoginMethod`, `Start`, `FlowState`,
`StepInput`, `Step` and `Proof` shapes in `pkg/authn`.

## Boundaries

- **The back-off is read before the derivation, and a key in back-off is answered
  without deriving.** `Check` runs first; a value that is not zero ends in
  `StepThrottled` carrying it. A failure is `Fail`-counted and refused, a success
  is `Succeed`-cleared, and a back-off that cannot be written is `Unavailable`
  and not a refusal — an attempt that would go uncounted is not a refusal to
  answer. A cancellation that lands after the back-off read does not go on to the
  derivation.
- **The back-off key is derived from the string the client sent, not the account
  it names.** It is the SHA-256 of the trimmed, case-folded identifier, so an
  identifier that exists and one that does not count along one path, and the
  stored key holds nothing a read of the table could use. Spellings the account
  module would treat as one identifier and this folding keeps apart count in
  separate keys here; the account-level back-off the capability keeps is what
  holds them together.
- **Every refusal of a credential reads the same and names nothing.** An unknown
  identifier, a wrong password and a disabled account all answer `StepRefused`
  carrying `authn.ErrInvalidCredentials`; a name the capability will not answer
  for is `Unavailable` and never a refusal. No answer's cause or text holds the
  identifier or the password.
- **A credential carrying `must_change` is not proven by the password alone.**
  The method answers `StepPrompt` for the new password, with a marked private
  state that says the old one was checked and holds neither password; only a new
  password the policy accepted, answered at that step, ends in a proof. A
  password the policy refuses is a 400 the flow keeps, not an end; a proof from
  this path carries `Proof.PasswordChanged`, which the engine acts on by revoking
  the account's other sessions.
- **The method serves `login` and `step-up` only.** A `link` binds an external
  identity and an `enroll` registers a factor, and a password proves neither, so
  a flow of another purpose is refused before the method reads or counts
  anything. An answer that is not a submitted form, and a step the method never
  named, are refused, so a flow record this method did not write cannot be walked
  into a proof.
- **The module requires the two capabilities it uses and nothing else.** The
  account module delivers `Passwords` and `Throttle`, each exclusively, so a
  command that names this method without an account module fails before anything
  is built. The login-method capability is delivered without being claimed, which
  is what lets several methods stand side by side.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/password/...
env -C pkg go test ./authn/login/password/...
env -C pkg go test -race ./authn/login/password/...
env -C pkg golangci-lint run ./authn/login/password/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no HTTP and no store: they build the method over a fake
`Passwords` and a fake `Throttle` whose answers and call order a case chooses and
reads back, and drive `Begin` and `Answer` directly. `logcapture_test.go` holds
that no cause the method produces puts an identifier or a password into a log
record, and `assembly_test.go` drives a real registry through the descriptor with
a stand-in account module for the two exclusive capabilities.
