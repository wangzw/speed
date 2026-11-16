/**
 * Playwright configuration for the demo starter's end-to-end tier.
 *
 * The tier drives the built demo binary -- a real sqlite file, real HTTP, the
 * review assembly's seeded operator -- through Chromium, and one journey also
 * through WebKit, with nothing but mouse and keyboard input. Its honesty rules
 * live in the specs' README and are enforced twice: by the eslint rule
 * `speed/no-e2e-bypass` over this directory and by `tools/check_e2e_honesty.py`
 * over the tree.
 *
 * The worker count is not fixed here: `make e2e` passes --workers from
 * GATE_JOBS, the CPU share the run was given, so the width is the caller's to
 * allocate and nothing probes the host's core count. There is no parallelism
 * inside a file: every spec starts its own backend in its own temporary
 * directory, with its own sqlite file and its own ports, and signs its own
 * accounts in, so journeys share no state. Files therefore run side by side,
 * while the tests inside one file stay serial (fullyParallel is off) because
 * they drive one page against one server. Probing for free ports can still
 * hand two workers the same port; the fixture retries a failed bind with fresh
 * ports, so the width costs a retry rather than a journey. A width near the
 * number of files keeps the run near the length of its longest journey, which
 * no width shortens.
 *
 * Artifacts and the binaries the fixtures start land under the run's own /tmp
 * root, never beside the source: a failure screenshot or trace is for the
 * operator running the tier, the honesty gate holds every artifact path to
 * /tmp so a passing run cannot leave a file in the tree, and the root carries
 * a per-run id, so two concurrent runs never share an output directory
 * (Playwright clears it on startup) or a harness binary. The front end is the
 * exception: an e2e-tagged binary embeds it, so the global setup builds it
 * into the git-ignored starters/demo/server/dist-e2e inside the checkout --
 * the one thing a run writes there.
 *
 * The run directory does not accumulate: global setup removes directories
 * older than the retention window before the run starts, and the last reporter
 * removes this run's directory once the whole run has passed. A failed or
 * interrupted run keeps it and the reporter prints where it is
 * (fixtures/retention-reporter.ts).
 */
import { defineConfig } from '@playwright/test'

import { ARTIFACT_ROOT } from './fixtures/paths'

export default defineConfig({
  testDir: './specs',
  globalSetup: './global-setup.ts',
  // The build runs once in the global setup, outside any test's bound. This
  // bound therefore covers one server's seed and startup, the operations
  // probes and a journey's clicks; the fixture bounds below stay strictly
  // inside it so the harness's own diagnostics (log tail, stderr) win over a
  // bare Playwright timeout. The width is the entry point's (--workers from
  // GATE_JOBS): several files at once, one journey at a time within a file.
  timeout: 120_000,
  fullyParallel: false,
  outputDir: `${ARTIFACT_ROOT}/artifacts`,
  reporter: [
    ['list'],
    [
      'html',
      { outputFolder: `${ARTIFACT_ROOT}/report`, open: 'never' },
    ],
    // Last, and deliberately so, although its own work is in `onExit`: every
    // reporter's `onEnd` -- the HTML report's included -- has finished by
    // then. It removes the run directory only when the run passed.
    ['./fixtures/retention-reporter.ts'],
  ],
  use: {
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    // A journey's expected copy is read from the en-US locale bundle, so the
    // browser is pinned to that language and to UTC rather than inheriting
    // whatever the machine running the tier happens to use.
    locale: 'en-US',
    timezoneId: 'UTC',
    // The account's first render follows the device's colour scheme (no
    // stored preference resolves to `system`), so pin it to light: the
    // first-paint background is then the same on every machine, and a
    // journey that flips the scheme opts in with page.emulateMedia.
    colorScheme: 'light',
  },
  // Chromium runs every journey: they exercise the rendered product, and the
  // bundled Chromium is the one the browser tier already pins. WebKit runs the
  // email password-reset journey alone, against the same http://localhost
  // server: it registers, resets and signs in, so it carries the flow, reset
  // and session cookies. WebKit, unlike Chromium and Firefox, refuses a
  // __Host- or __Secure- cookie set over http even on a loopback host, and
  // WebKit on Linux refuses any Secure cookie set over http, loopback host
  // included. The journey passing there is what holds the cookies of a
  // loopback http deployment, unprefixed and without Secure, to what WebKit
  // keeps. `make e2e-install` installs both.
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    {
      name: 'webkit',
      use: { browserName: 'webkit' },
      testMatch: /journey-password-reset-email\.spec\.ts$/,
    },
  ],
})
