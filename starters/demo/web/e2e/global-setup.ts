/**
 * The one build the whole run shares, and the sweep that bounds the artifacts.
 *
 * The run's own directory is created here, so this is also where the retention
 * rule runs: before the harness directory is created, every earlier run
 * directory under /tmp whose modification time is older than the retention
 * window is removed. The per-spec directories a failed test keeps -- the
 * review server's temporary directory and the deploy fixture's volume -- are
 * swept by the same age rule under the system temp directory, each by its own
 * name shape. A fresh directory -- this run's, or one belonging to a run
 * happening in parallel -- is inside the window and is never touched.
 *
 * The Go program embeds starters/demo/server/dist-e2e in an e2e-tagged build,
 * so the front end is built first; the review binary is built second and lands
 * in the harness directory under /tmp. A manifest records where the binary is
 * and which tree it came from, and every per-test server fixture starts that
 * same binary. Building here rather than in each fixture keeps a run from
 * repeating a minute of compilation per test, and keeps the build's output out
 * of the tree.
 *
 * A failed build fails the run with the command's whole output: a harness that
 * silently fell back to a stale binary would report journeys it never ran.
 */
import { execFile } from 'node:child_process'
import { mkdir, readdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import {
  ARTIFACT_ROOT,
  BINARY_NAME,
  DEPLOY_BINARY_NAME,
  HARNESS_DIR,
  MANIFEST_PATH,
  REPO_ROOT,
  SAML_SIGNER_NAME,
  SERVER_DIR,
  WEB_DIR,
} from './fixtures/paths'
import { FRONT_END_BUILD_ARGS } from './fixtures/front-end-build'
import {
  removeExpiredPerSpecDirectories,
  removeExpiredRunDirectories,
} from './fixtures/retention'

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
  env: NodeJS.ProcessEnv = {},
): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(
      file,
      [...args],
      {
        cwd,
        env: { ...process.env, ...env },
        timeout: BUILD_TIMEOUT_MS,
        maxBuffer: 32 * 1024 * 1024,
      },
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
  // runs that have already finished (or died) more than a day ago.
  const expired = await removeExpiredRunDirectories()
  for (const path of expired) {
    process.stderr.write(`[e2e] removed expired run directory: ${path}\n`)
  }
  const expiredPerSpec = await removeExpiredPerSpecDirectories()
  for (const path of expiredPerSpec) {
    process.stderr.write(`[e2e] removed expired per-spec directory: ${path}\n`)
  }

  await mkdir(HARNESS_DIR, { recursive: true })

  // The front end first: the Go program embeds the directory the build writes,
  // and an e2e-tagged build embeds server/dist-e2e rather than the dev tree's
  // server/dist, so a binary built before the bundle carries nothing. The
  // variable keeps this build off the tree `make dev` is using.
  //
  // The directory is emptied first: vite's empty-outdir safeguard keeps the
  // placeholders go:embed needs, so a build over a previous bundle leaves the
  // stale hashed files in place. Emptying it makes each binary embed exactly
  // what this run built. The tracked placeholder.txt stays, because go:embed
  // refuses a directory with no files and a clean checkout has nothing else
  // here until this build writes it. server/dist is untouched.
  const bundleDir = join(SERVER_DIR, 'dist-e2e')
  await mkdir(bundleDir, { recursive: true })
  const stale = await readdir(bundleDir)
  await Promise.all(
    stale
      .filter((name) => name !== 'placeholder.txt')
      .map((name) => rm(join(bundleDir, name), { recursive: true, force: true })),
  )
  await build(
    'pnpm',
    FRONT_END_BUILD_ARGS,
    REPO_ROOT,
    'building the demo front end',
    { DEMO_WEB_OUT_DIR: join(SERVER_DIR, 'dist-e2e') },
  )

  const binary = join(HARNESS_DIR, BINARY_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', '-tags', 'review,e2e', '-o', binary, '.'],
    REPO_ROOT,
    'building the review binary',
  )

  // The deployment build is the default build: every capability is compiled in,
  // and what the deployment starts is decided at run time from the rendered
  // configuration. Only the e2e tag is added, which switches the embedded bundle
  // to dist-e2e; it embeds the same front end, so it is built after the build
  // like the review binary. The review-only test controls and loopback guard are
  // absent from it, which is what keeps the deployment journey's feature set the
  // deployment's own.
  const deployBinary = join(HARNESS_DIR, DEPLOY_BINARY_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', '-tags', 'e2e', '-o', deployBinary, '.'],
    REPO_ROOT,
    'building the default binary',
  )

  // The SAML identity provider fixture signs its responses with the library the
  // product validates them with, through this small command. It is built beside
  // the binaries and used only by the spec that drives that provider.
  const samlSigner = join(HARNESS_DIR, SAML_SIGNER_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', '-o', samlSigner, './e2e/samlsign'],
    REPO_ROOT,
    'building the SAML signing helper',
  )

  const manifest = {
    binary,
    deployBinary,
    samlSigner,
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
