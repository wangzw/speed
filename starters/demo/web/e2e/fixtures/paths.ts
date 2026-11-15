/**
 * The paths the end-to-end harness resolves once and shares.
 *
 * Every artifact of a run lives under one per-run root: two runs in different
 * worktrees would otherwise share the same harness binary, manifest and
 * Playwright outputDir, and one run's Playwright startup would clear the
 * other's failure screenshots. The run id is minted once and carried in the
 * environment, so the config process, the global setup and every fixture
 * worker agree on it even though they are separate processes. The front-end
 * build is the exception: it writes `starters/demo/server/dist-e2e` inside the
 * checkout and both binaries embed that directory, so at most one run may
 * execute in one checkout at a time. Two checkouts are isolated because each
 * owns its own `dist-e2e`.
 *
 * The root, the harness build and the manifest stay under /tmp: a run must
 * leave nothing in the tree except the git-ignored bundle its front-end build
 * writes (starters/demo/server/dist-e2e, which the e2e-tagged binaries
 * embed). The honesty gate (tools/check_e2e_honesty.py) holds the /tmp
 * requirement.
 *
 * Every path is derived from this file's own URL rather than the process's
 * working directory: the tier is run through `pnpm -C starters/demo/web`, but a
 * person may also run `playwright test` from the config's directory, and the
 * two must resolve the same tree.
 */
import { randomBytes } from 'node:crypto'
import { fileURLToPath } from 'node:url'

/** The environment variable that carries one run's id across processes. */
export const RUN_ID_ENV = 'SPEED_E2E_RUN_ID'

/** Mint (or reuse) the id every process of this run shares. */
function resolveRunId(): string {
  const fromEnvironment = process.env[RUN_ID_ENV]
  if (fromEnvironment !== undefined && fromEnvironment !== '') {
    return fromEnvironment
  }
  const minted = `${process.pid}-${Date.now().toString(36)}-${randomBytes(4).toString('hex')}`
  process.env[RUN_ID_ENV] = minted
  return minted
}

/** This run's id: unique per invocation, shared by every process it starts. */
export const RUN_ID = resolveRunId()

/**
 * The root every artifact of a run lives under. The Playwright config composes
 * `outputDir` and the HTML reporter's folder from it, and the harness keeps
 * its one build there too.
 */
export const ARTIFACT_ROOT = `/tmp/speed-e2e-${RUN_ID}`

/** The harness's own directory: the build and the manifest that names it. */
export const HARNESS_DIR = `${ARTIFACT_ROOT}/harness`

/** The manifest the global setup writes and the server fixture reads. */
export const MANIFEST_PATH = `${HARNESS_DIR}/manifest.json`

/** The review binary the global setup builds. */
export const BINARY_NAME = 'demo-review'

/** The deploy assembly's binary: the default build, with no review controls. */
export const DEPLOY_BINARY_NAME = 'demo-deploy'

/** The helper that signs the SAML identity provider's responses. */
export const SAML_SIGNER_NAME = 'saml-signer'

/** The repository root. */
export const REPO_ROOT = fileURLToPath(new URL('../../../../../', import.meta.url))

/** The demo program's Go module directory. */
export const SERVER_DIR = fileURLToPath(new URL('../../../server/', import.meta.url))

/** The demo starter's front-end package directory. */
export const WEB_DIR = fileURLToPath(new URL('../../', import.meta.url))

/** The review assembly's configuration template, copied and edited per server. */
export const REVIEW_CONFIG_TEMPLATE = fileURLToPath(
  new URL('../../../server/review/sqlite.yaml', import.meta.url),
)

/**
 * The deployment's configuration template. The container entrypoint renders it
 * from Fly secrets; the deploy fixture renders the same placeholders itself and
 * runs the rendered document from the temporary volume.
 */
export const DEPLOY_CONFIG_TEMPLATE = fileURLToPath(
  new URL('../../../deploy/fly.yaml.tmpl', import.meta.url),
)
