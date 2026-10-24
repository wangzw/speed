/**
 * The one build the whole run shares, and the sweep that bounds the artifacts.
 *
 * The run's own directory is created here, so this is also where the retention
 * rule runs: before the harness directory is created, every earlier run
 * directory under /tmp whose modification time is older than the retention
 * window is removed. The sweep matches the whole run directory name and only
 * direct children of /tmp, so the per-spec server directories beside it are
 * left alone. A fresh directory -- this run's, or one belonging to a run
 * happening in parallel -- is inside the window and is never touched.
 *
 * The Go program embeds starters/demo/server/dist, so the front end is built
 * first; the review binary is built second and lands in the harness directory
 * under /tmp. A manifest records where the binary is and which tree it came
 * from, and every per-test server fixture starts that same binary. Building
 * here rather than in each fixture keeps a run from repeating a minute of
 * compilation per test, and keeps the build's output out of the tree.
 *
 * A failed build fails the run with the command's whole output: a harness that
 * silently fell back to a stale binary would report journeys it never ran.
 */
import { execFile } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import {
  ARTIFACT_ROOT,
  BINARY_NAME,
  DEPLOY_BINARY_NAME,
  HARNESS_DIR,
  MANIFEST_PATH,
  REPO_ROOT,
  SERVER_DIR,
  WEB_DIR,
} from './fixtures/paths'
import { removeExpiredRunDirectories } from './fixtures/retention'

const BUILD_TIMEOUT_MS = 10 * 60_000

/** A build-output line that reports a warning. */
const WARNING_LINE = /(^|[\s[])(warning|warn)(:|\b)/i

/** The warnings a build printed, whichever stream carried them. */
function warningLines(output: string): string[] {
  return output
    .split('\n')
    .map((line) => line.trimEnd())
    .filter((line) => line !== '' && WARNING_LINE.test(line))
}

/**
 * Run a build command. A failure carries the whole output. A success is not
 * silent either: standard error is printed, and a warning in either stream
 * fails the run, so a bundler directive that only warns is seen rather than
 * swallowed by the harness.
 */
function build(
  file: string,
  args: readonly string[],
  cwd: string,
  what: string,
): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(
      file,
      [...args],
      { cwd, env: process.env, timeout: BUILD_TIMEOUT_MS, maxBuffer: 32 * 1024 * 1024 },
      (error, stdout, stderr) => {
        // Standard error is diagnostics even when the command succeeded.
        if (stderr.trim() !== '') {
          process.stderr.write(stderr.endsWith('\n') ? stderr : `${stderr}\n`)
        }
        if (error !== null) {
          reject(
            new Error(
              `${what} failed: ${error.message}\nstandard output:\n${stdout}\nstandard error:\n${stderr}`,
            ),
          )
          return
        }
        const warnings = [...warningLines(stdout), ...warningLines(stderr)]
        if (warnings.length > 0) {
          reject(
            new Error(
              `${what} reported warning(s); warnings are first-class and fail ` +
                `the harness until they are fixed:\n${warnings.join('\n')}`,
            ),
          )
          return
        }
        resolve()
      },
    )
  })
}

export default async function globalSetup(): Promise<void> {
  // Sweep before the harness directory is created: the directory about to be
  // made is fresh and therefore outside the window, so the only candidates are
  // runs that have already finished (or died) more than a week ago.
  const expired = await removeExpiredRunDirectories()
  for (const path of expired) {
    process.stderr.write(`[e2e] removed expired run directory: ${path}\n`)
  }

  await mkdir(HARNESS_DIR, { recursive: true })

  // The front end first: the Go program embeds the dist directory the build
  // writes, so a binary built before it would carry the previous bundle.
  await build(
    'pnpm',
    ['-C', WEB_DIR, 'build'],
    REPO_ROOT,
    'building the demo front end',
  )

  const binary = join(HARNESS_DIR, BINARY_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', '-tags', 'review', '-o', binary, '.'],
    REPO_ROOT,
    'building the review binary',
  )

  // The deployment assembly is a second build of the same program: the deploy
  // tag adds the SMTP carrier, the email verification code sender and the two
  // social login methods, which is what the deploy journey drives. It embeds
  // the same front-end bundle, so it is built after the front end like the
  // review binary.
  const deployBinary = join(HARNESS_DIR, DEPLOY_BINARY_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', '-tags', 'deploy', '-o', deployBinary, '.'],
    REPO_ROOT,
    'building the deploy binary',
  )

  const manifest = {
    binary,
    deployBinary,
    artifactsRoot: ARTIFACT_ROOT,
    repoRoot: REPO_ROOT,
    serverDir: SERVER_DIR,
    webDir: WEB_DIR,
    builtAt: new Date().toISOString(),
  }
  await writeFile(
    MANIFEST_PATH,
    `${JSON.stringify(manifest, null, 2)}\n`,
    { encoding: 'utf8', mode: 0o600 },
  )
}
