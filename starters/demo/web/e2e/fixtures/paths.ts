import { randomBytes } from 'node:crypto'
import { fileURLToPath } from 'node:url'

export const RUN_ID_ENV = 'SPEED_E2E_RUN_ID'

function resolveRunId(): string {
  const fromEnvironment = process.env[RUN_ID_ENV]
  if (fromEnvironment !== undefined && fromEnvironment !== '') {
    return fromEnvironment
  }
  const minted = `${process.pid}-${Date.now().toString(36)}-${randomBytes(4).toString('hex')}`
  process.env[RUN_ID_ENV] = minted
  return minted
}

export const RUN_ID = resolveRunId()

export const ARTIFACT_ROOT = `/tmp/speed-e2e-${RUN_ID}`

export const HARNESS_DIR = `${ARTIFACT_ROOT}/harness`

export const MANIFEST_PATH = `${HARNESS_DIR}/manifest.json`

export const BINARY_NAME = 'demo-e2e'

export const FACTOR_SEED_NAME = 'factorseed'

export const SAML_SIGNER_NAME = 'saml-signer'

// The reference is a parameter, not a literal in each `new URL(..., import.meta.url)`: vite rewrites the literal form into a dev-server asset URL under the jsdom transform.
function fromHere(reference: string): string {
  return fileURLToPath(new URL(reference, import.meta.url))
}

export const REPO_ROOT = fromHere('../../../../../')

export const SERVER_DIR = fromHere('../../../server/')

export const WEB_DIR = fromHere('../../')

export const WEB_WORKSPACE_DIR = fromHere('../../../../../web/')

export const E2E_CONFIG_TEMPLATE = fromHere('../../../server/e2e/config.yaml')

export const DEPLOY_CONFIG_TEMPLATE = fromHere('../../../deploy/fly.yaml.tmpl')
