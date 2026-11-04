---
name: browser-review
description: Persistent headed browser review of the demo. Load this before the generic playwright-cli skill whenever the task is to open the demo in a browser, review a UI change in the running app, smoke-test a journey, sign in to the demo, or take screenshots at http://localhost:8080. Every command carries -s=speed-review, and open carries the fixed profile .playwright-cli/review-profile.
triggers:
  - browser review
  - open the demo in a browser
  - headed browser review
  - review the demo UI
  - smoke-test the demo
  - sign in to the demo
  - screenshot localhost:8080
  - playwright-cli review
allowed-tools: Bash(playwright-cli:*)
globs:
  - "starters/demo/**"
---

# Browser review

One persistent browser, one profile, reused across restarts. The profile
directory is the browser's identity; the session name is not.

## The one command

```sh
playwright-cli -s=speed-review open http://localhost:8080 \
  --profile="$PWD/.playwright-cli/review-profile" --headed
```

Run it from the repository root: `$PWD` must be the checkout root, because
the profile lives in that checkout's `.playwright-cli/review-profile`.

Every other command carries the session name alone:

```sh
playwright-cli -s=speed-review snapshot
playwright-cli -s=speed-review goto http://localhost:8080/settings
```

## Resume, do not reopen

`open` stops the session of the same name before starting a new one. Check
first:

```sh
playwright-cli -s=speed-review list
```

- **running** — use `goto`, `snapshot`, `click` and the rest directly. Do
  not `open`; that closes every tab the review depends on.
- **stopped** — run the one command again. The profile keeps the cookies,
  so the logins survive; the open tabs do not.

`list` shows running and stopped persistent sessions alike. A stopped entry
is not a browser: `goto` against it fails, and the correct move is `open`.

## Local only

- Target `http://localhost:8080` only. `starters/demo/server/local.yaml`
  sets the public URL and the WebAuthn origins to that exact spelling;
  `127.0.0.1` is a different origin and carries no cookies.
- Never point this profile at the Fly deployment. Local and remote are
  separate origins with separate databases, signing keys and accounts;
  mixing them muddies both. A remote review needs its own session and its
  own profile directory.

## The first sign-in is manual

Sign in by hand in the headed window: the demo operator, GitHub, Google.
The profile keeps those cookies across restarts, so this happens once.

- The development operator is defined in
  `starters/demo/server/defaults.go`; the seeded configurations under
  `starters/demo/server/testdata/` carry the same credentials. Read them
  there. Never copy a password into a document, a skill or a commit.
- A verification code is in the local process output: the `log sms:` or
  `log mail:` line, field `verification_code`. Reading it is expected.
- Passkeys run through the browser's native panel. Do not script around it.

## Preconditions

- `make dev` serves the app at `http://localhost:8080` (vite, strictPort)
  and proxies the API to `127.0.0.1:8081`. A taken port fails the start
  rather than moving; stop whatever holds it.
- Do not run `make e2e` while `make dev` is up: the e2e global setup
  rebuilds `starters/demo/server/dist`, which the running program embeds.
- Do not start the `-tags=review` assembly while `make dev` is up. It binds
  `127.0.0.1:8080` too.

## Driving the review

Interaction is clicks and keyboard input, following the honesty rules in
`starters/demo/web/e2e/README.md`:

- no `fetch`, XHR or node HTTP from the page, no `page.evaluate` to stage
  state, no route mocking;
- never inject rows into the database to manufacture a precondition; a
  state the UI cannot reach is recorded as unreachable;
- assert on what the product renders, not on an API response alone;
- reading a verification code from the log is the sanctioned read. It does
  not replace a UI assertion.

## Profile hygiene

- The profile lives at `.playwright-cli/review-profile` and stays there.
  `.playwright-cli/` is ignored by git and never committed.
- `state-save` writes into `.playwright-cli/` too, always by absolute path.
- One profile, one browser at a time. A second instance on the same profile
  fails on Chromium's lock; a concurrent review uses another session name
  and another profile directory.
- `playwright-cli delete-data` removes the session record, not an explicit
  `--profile` directory. A real reset needs the session closed and the
  user's agreement. Do not delete `.playwright-cli/` to clear artifacts and
  do not run `git clean -xfd`: both take the logins with them.
- A worktree has its own `.playwright-cli/`, so it starts signed out.

## Anti-patterns

- `playwright-cli open` with no `-s`, or with `--persistent` instead of
  `--profile`.
- `-s=speed-review` without `--profile`: same name, different profile,
  signed out again.
- `open` on a session that is already running.
- Any command against `https://vislake-speed-demo.fly.dev` from this
  session.
- `state-save` into the repository root.
