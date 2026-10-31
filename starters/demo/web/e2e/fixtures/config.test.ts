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

import { writeReviewConfig, type ReviewConfigInput } from './config'

// The fixture paths derive from `import.meta.url`, which the jsdom transform
// does not turn into a file URL, so this suite names the templates from the
// package's working directory instead. Vitest runs from the package directory.
vi.mock('./paths', () => ({
  REVIEW_CONFIG_TEMPLATE: `${process.cwd()}/../server/review/sqlite.yaml`,
  DEPLOY_CONFIG_TEMPLATE: `${process.cwd()}/../server/deploy/fly.yaml.tmpl`,
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
    // journey's server. The factors stay on: the impersonation option seeds
    // them and turns the switch on, and nothing else.
    expect(body).toContain(
      '    - name: acme\n      admins:\n        - member\n  members:',
    )
    expect(body).toContain('\n  impersonation: true\n')
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
