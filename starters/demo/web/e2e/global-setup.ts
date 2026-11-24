import { execFile } from 'node:child_process'
import { mkdir, readdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import {
  ARTIFACT_ROOT,
  BINARY_NAME,
  FACTOR_SEED_NAME,
  HARNESS_DIR,
  MANIFEST_PATH,
  REPO_ROOT,
  SAML_SIGNER_NAME,
  SERVER_DIR,
  WEB_DIR,
} from './fixtures/paths'
import { FRONT_END_BUILD_ARGS } from './fixtures/front-end-build'
import { GATE_JOBS_ENV, parseGateJobs } from './fixtures/gate-jobs'
import {
  removeExpiredPerSpecDirectories,
  removeExpiredRunDirectories,
} from './fixtures/retention'

const BUILD_TIMEOUT_MS = 10 * 60_000

const WARNING_LINE = /(^|[\s[])(warning|warn)(:|\b)/i

function warningLines(output: string): string[] {
  return output
    .split('\n')
    .map((line) => line.trimEnd())
    .filter((line) => line !== '' && WARNING_LINE.test(line))
}

// A successful build still prints standard error, and a warning in either stream fails the run.
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
  const width = parseGateJobs(process.env[GATE_JOBS_ENV])

  const expired = await removeExpiredRunDirectories()
  for (const path of expired) {
    process.stderr.write(`[e2e] removed expired run directory: ${path}\n`)
  }
  const expiredPerSpec = await removeExpiredPerSpecDirectories()
  for (const path of expiredPerSpec) {
    process.stderr.write(`[e2e] removed expired per-spec directory: ${path}\n`)
  }

  await mkdir(HARNESS_DIR, { recursive: true })

  // The front end must be built before the Go binary embeds it, into server/dist-e2e rather than the dev tree. Empty the directory first: vite's empty-outdir safeguard keeps the placeholders go:embed needs, so a build over a previous bundle would leave stale hashed files. The tracked placeholder.txt stays because go:embed refuses an empty directory.
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
    ['-C', SERVER_DIR, 'build', `-p=${width}`, '-tags', 'e2e', '-o', binary, '.'],
    REPO_ROOT,
    'building the e2e binary',
    { GOMAXPROCS: String(width) },
  )

  const samlSigner = join(HARNESS_DIR, SAML_SIGNER_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', `-p=${width}`, '-o', samlSigner, './e2e/samlsign'],
    REPO_ROOT,
    'building the SAML signing helper',
    { GOMAXPROCS: String(width) },
  )

  const factorSeed = join(HARNESS_DIR, FACTOR_SEED_NAME)
  await build(
    'go',
    ['-C', SERVER_DIR, 'build', `-p=${width}`, '-o', factorSeed, './e2e/factorseed'],
    REPO_ROOT,
    'building the factor seeding helper',
    { GOMAXPROCS: String(width) },
  )

  const manifest = {
    binary,
    factorSeed,
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
