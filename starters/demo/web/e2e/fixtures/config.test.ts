/**
 * The review configuration's enterprise SSO block.
 *
 * The template states the deployment defaults -- the loopback relaxation off
 * and both outbound seams empty -- and the server fixture replaces the block
 * whole when a spec opts into the local fixtures. Both directions are held
 * here: an unconfigured server keeps the deployment defaults, and an opted-in
 * one names the resolver and the CA file it was handed.
 */
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  reviewImpersonationAdmin,
  reviewOrdinaryMember,
  reviewSystemTenantAdmin,
  reviewTenantAdmin,
  reviewTenantSetting,
  reviewUnprovedTenantAdmin,
  writeDeployConfig,
  writeReviewConfig,
  type ReviewConfigInput,
} from './config'

// The fixture paths derive from `import.meta.url`, which the jsdom transform
// does not turn into a file URL, so this suite names the templates from the
// package's working directory instead. Vitest runs from the package directory.
vi.mock('./paths', () => ({
  REVIEW_CONFIG_TEMPLATE: `${process.cwd()}/../server/review/sqlite.yaml`,
  DEPLOY_CONFIG_TEMPLATE: `${process.cwd()}/../deploy/fly.yaml.tmpl`,
}))

const dirs: string[] = []

afterEach(async () => {
  const kept = dirs.splice(0)
  await Promise.all(kept.map((dir) => rm(dir, { recursive: true, force: true })))
})

type TemplateInput = Omit<ReviewConfigInput, 'dir' | 'backendPort'>

async function rendered(input: TemplateInput): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'speed-e2e-config-'))
  dirs.push(dir)
  const config = await writeReviewConfig({
    dir,
    backendPort: 41080,
    ...input,
  })
  return readFile(config.path, 'utf8')
}

/**
 * The rendered `seed.accounts` entry that names an identifier, from its own
 * `- identifier:` line to the next entry. A test uses it to read the flags a
 * single account carries without matching a sibling's lines.
 */
function accountEntry(body: string, identifier: string): string {
  const needle = `    - identifier: ${JSON.stringify(identifier)}`
  const at = body.indexOf(needle)
  if (at === -1) {
    throw new Error(`the rendered configuration holds no account ${identifier}`)
  }
  const rest = body.slice(at + needle.length)
  const nextAt = rest.search(/^ {4}- identifier:/m)
  const end = nextAt === -1 ? body.length : at + needle.length + nextAt
  return body.slice(at, end)
}

describe('the review configuration', () => {
  it('renders one listener and moves every origin onto its port', async () => {
    const body = await rendered({})
    expect(body).toContain('    backend:\n      address: "127.0.0.1:41080"')
    expect(body).not.toContain('    ops:\n')
    expect(body).not.toContain('8080')
    expect(body).not.toContain('8083')
    expect(body.match(/"http:\/\/localhost:41080"/g)).toHaveLength(3)
  })

  it('keeps the enterprise SSO seams at the deployment defaults', async () => {
    const body = await rendered({})
    expect(body).toContain(
      '    allow-private-egress: false\n    dns-resolver: ""\n    oidc-ca-file: ""',
    )
    expect(body).toContain(
      '    - name: acme\n      admins:\n        - member\n        - operator@demo.test',
    )
    expect(body).not.toContain('globex')
  })

  it('points the enterprise SSO seams at the local fixtures on request', async () => {
    const caFile = '/tmp/speed-e2e-enterprise/ca.pem'
    const body = await rendered({
      ssoProvider: { dnsResolver: '127.0.0.1:5353', rootCAFile: caFile },
    })
    expect(body).toContain('    allow-private-egress: true')
    expect(body).toContain('    dns-resolver: "127.0.0.1:5353"')
    expect(body).toContain(`    oidc-ca-file: ${JSON.stringify(caFile)}`)
    expect(body).not.toContain('    allow-private-egress: false')
  })

  it('keeps the operator out of acme while it seeds the impersonation factors', async () => {
    const body = await rendered({ seedImpersonation: true })
    // The impersonation requirement is a target inside the tenant and an
    // initiator outside it, so the operator holds no grant in acme on this
    // journey's server. The target is an ordinary member, not the tenant's
    // administrator, so an account of the fixture's own administers acme.
    expect(body).toContain(
      `    - name: acme\n      admins:\n        - ${reviewImpersonationAdmin.identifier}\n  members:`,
    )
    expect(body).not.toContain(`        - ${reviewOrdinaryMember.identifier}\n`)
    expect(body).toContain(
      `    - identifier: ${JSON.stringify(reviewImpersonationAdmin.identifier)}`,
    )
    expect(body).toContain('\n  impersonation: true\n')
  })

  it('gives a tenant exactly the administrators the override names', async () => {
    const body = await rendered({
      seedTenantAdmin: true,
      seedUnprovedTenantAdmin: true,
      tenantAdmins: { acme: [reviewTenantAdmin.identifier] },
    })
    // Both administrators are seeded, but the override pins acme to one: not
    // the unproved administrator the default list would add, the template's
    // member, or the platform operator.
    expect(body).toContain(
      `    - name: acme\n      admins:\n        - ${reviewTenantAdmin.identifier}\n  members:`,
    )
    expect(body).not.toContain(`        - ${reviewUnprovedTenantAdmin.identifier}\n`)
    expect(body).not.toContain(`        - ${reviewOrdinaryMember.identifier}\n`)
    expect(body).not.toContain('        - operator@demo.test\n')
  })

  it('seeds the proved-email administrator as the tenant\'s only administrator', async () => {
    const body = await rendered({ seedTenantAdmin: true })
    expect(body).toContain(
      `    - name: acme\n      admins:\n        - ${reviewTenantAdmin.identifier}\n  members:`,
    )
    // The address is the one the review build proves; the account carries the
    // username it signs in with and no platform-administrator mark.
    expect(body).toContain(
      `    - identifier: ${JSON.stringify(reviewTenantAdmin.identifier)}\n` +
        `      username: ${JSON.stringify(reviewTenantAdmin.username)}\n`,
    )
    expect(body).toContain('      proved-email: true')
    const entry = accountEntry(body, reviewTenantAdmin.identifier)
    expect(entry).not.toContain('platform-admin')
  })

  it('seeds the unproved-email administrator without a proof', async () => {
    const body = await rendered({ seedUnprovedTenantAdmin: true })
    expect(body).toContain(
      `    - name: acme\n      admins:\n        - ${reviewUnprovedTenantAdmin.identifier}\n  members:`,
    )
    const entry = accountEntry(body, reviewUnprovedTenantAdmin.identifier)
    expect(entry).toContain('username:')
    expect(entry).not.toContain('proved-email')
    expect(entry).not.toContain('platform-admin')
  })

  it('seeds a system tenant administrator outside the platform role', async () => {
    const body = await rendered({ seedSystemTenantAdmin: true })
    expect(body).toContain(
      `\n  system-admins:\n    - ${reviewSystemTenantAdmin.identifier}\n  members:\n`,
    )
    const entry = accountEntry(body, reviewSystemTenantAdmin.identifier)
    expect(entry).not.toContain('platform-admin')
  })

  it('names a declared tenant-writable setting outside the platform-only namespaces', () => {
    expect(reviewTenantSetting.key).toBe(
      `${reviewTenantSetting.namespace}.per-recipient-hourly`,
    )
    for (const platformOnly of ['ratelimit', 'tenant.admission', 'authn']) {
      expect(reviewTenantSetting.namespace).not.toMatch(
        new RegExp(`^${platformOnly.replace('.', '\\.')}(\\.|$)`),
      )
    }
  })

  it("registers the enterprise journey's second organization on request", async () => {
    const body = await rendered({
      ssoProvider: { dnsResolver: '127.0.0.1:5353', rootCAFile: '/tmp/ca.pem' },
    })
    expect(body).toContain(
      '    - name: globex\n      admins:\n        - operator@demo.test',
    )
    expect(body.match(/^    - name: globex$/gm)).toHaveLength(1)
  })
})

describe('the deployment configuration', () => {
  it('renders every placeholder, the Turnstile action included', async () => {
    const volume = await mkdtemp(join(tmpdir(), 'speed-e2e-deploy-'))
    dirs.push(volume)
    const config = await writeDeployConfig({
      volume,
      backendPort: 41081,
      opsPort: 41082,
    })
    const body = await readFile(config.path, 'utf8')
    // A leftover placeholder fails writeDeployConfig itself; this states the
    // whole document, the action included, is rendered.
    expect(body).not.toMatch(/__[A-Z][A-Z0-9_]*__/)
    // The entrypoint defaults the widget action to login, so the fixture
    // renders the same action a deployment without the secret would carry.
    expect(body).toContain('    action: "login"')
  })
})
