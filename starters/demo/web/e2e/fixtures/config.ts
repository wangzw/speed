import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import {
  enterpriseIdentityProviderUser,
  identityProviderUser,
} from './identity-provider'
import { DEPLOY_CONFIG_TEMPLATE, E2E_CONFIG_TEMPLATE } from './paths'

export interface ServerConfigInput {
  readonly dir: string
  readonly backendPort: number
  readonly seedMember?: boolean
  readonly seedImpersonation?: boolean
  readonly seedInvitee?: boolean
  readonly invitationMail?: boolean
  readonly seedSocialAccount?: boolean
  readonly socialProvider?: {
    readonly issuer: string
    readonly rootCAFile: string
  }
  readonly ssoProvider?: {
    readonly dnsResolver: string
    readonly rootCAFile: string
  }
  readonly recentWindow?: string
  readonly mfaRequired?: boolean
  readonly identityQuota?: string
  readonly tenantSSO?: boolean
  readonly seedTenantAdmin?: boolean
  readonly seedUnprovedTenantAdmin?: boolean
  readonly seedSystemTenantAdmin?: boolean
  readonly platformRequireMFA?: boolean
  readonly tenantRequireMFA?: boolean
  readonly tenantAdmins?: Readonly<Record<string, readonly string[]>>
}

export interface ServerConfig {
  readonly path: string
  readonly dir: string
  readonly backendPort: number
  readonly backendAddress: string
  readonly origin: string
  readonly dsn: string
  readonly signingKey: string
  readonly objectsRoot: string
}

export const e2eMember = {
  tenant: 'acme',
  identifier: 'member@demo.test',
  username: 'member-e2e',
  password: 'member-e2e-secret',
  displayName: 'Demo Member',
} as const

export const smsRolloutOperator = {
  tenant: 'acme',
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

export const e2eInvitee = {
  identifier: 'invitee@demo.test',
  username: 'invitee-e2e',
  password: 'invitee-e2e-secret',
  displayName: 'Invitation Invitee',
} as const

export const e2eSocialAccount = {
  identifier: identityProviderUser.email,
  username: 'social-signin',
  password: 'social-e2e-secret',
  displayName: identityProviderUser.name,
} as const

export const tenantSSOTenant = 'globex'

export const tenantSSOAdmin = {
  identifier: enterpriseIdentityProviderUser.email,
  username: 'globex-admin',
  password: 'tenant-sso-e2e-secret',
  displayName: 'Globex Administrator',
} as const

export const e2eTenantAdmin = {
  tenant: 'acme',
  identifier: 'tenant-admin@demo.test',
  username: 'tenant-admin-e2e',
  password: 'tenant-admin-e2e-secret',
  displayName: 'Tenant Administrator',
} as const

export const e2eUnprovedTenantAdmin = {
  tenant: 'acme',
  identifier: 'unproved-admin@demo.test',
  username: 'unproved-admin-e2e',
  password: 'unproved-admin-e2e-secret',
  displayName: 'Unproved Tenant Administrator',
} as const

export const e2eSystemTenantAdmin = {
  tenant: 'system',
  identifier: 'system-admin-e2e',
  password: 'system-admin-e2e-secret',
  displayName: 'System Tenant Administrator',
} as const

export const e2eOrdinaryMember = {
  tenant: 'acme',
  identifier: 'member',
  password: 'member-dev-secret',
  displayName: 'Demo Member',
} as const

export const e2eImpersonationAdmin = {
  tenant: 'acme',
  identifier: 'acme-owner',
  password: 'acme-owner-secret',
  displayName: 'Acme Owner',
} as const

export const e2eImpersonationFactors = {
  account: 'operator',
  totpSeed: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
  totpAge: '2h',
  smsAge: '0s',
} as const

// tenant.admission is the only tenant-facing namespace; every other declared namespace is platform-level.
export const e2eTenantPolicies = {
  namespace: 'tenant.admission',
  keys: [
    'tenant.admission.require-mfa',
    'tenant.admission.require-sso',
  ],
  platformOnlyNamespaces: [
    'ratelimit',
    'authn',
    'jobs.db',
    'notification.delivery',
    'objectstore.metadata',
    'sms',
  ],
  platformKeys: [
    'jobs.db.poll-interval',
    'jobs.db.claim-batch',
    'jobs.db.lease',
    'jobs.db.retry-base',
    'jobs.db.retry-max',
    'notification.delivery.per-recipient-hourly',
    'notification.delivery.per-recipient-daily',
    'notification.delivery.global-hourly',
    'notification.delivery.tenant-distinct-hourly',
    'notification.delivery.attempt-hourly',
    'objectstore.metadata.upload-window',
    'objectstore.metadata.object-ttl',
    'objectstore.metadata.url-ttl',
    'sms.ceiling-hourly',
    'sms.registry.sending-enabled',
  ],
} as const

const seedOperatorAccount = `    - identifier: operator@demo.test
      username: operator
      password: operator-dev-secret
      display-name: Demo Operator
      platform-admin: true`

function seedMemberAccount(): string {
  return `    - identifier: ${quoted(e2eMember.identifier)}
      username: ${quoted(e2eMember.username)}
      password: ${quoted(e2eMember.password)}
      display-name: ${quoted(e2eMember.displayName)}`
}

function seedMemberMembership(): string {
  return `    - tenant: ${quoted(e2eMember.tenant)}
      account: ${quoted(e2eMember.identifier)}`
}

function seedSocialAccount(): string {
  return `    - identifier: ${quoted(e2eSocialAccount.identifier)}
      username: ${quoted(e2eSocialAccount.username)}
      password: ${quoted(e2eSocialAccount.password)}
      display-name: ${quoted(e2eSocialAccount.displayName)}`
}

function seedInviteeAccount(): string {
  return `    - identifier: ${quoted(e2eInvitee.identifier)}
      username: ${quoted(e2eInvitee.username)}
      password: ${quoted(e2eInvitee.password)}
      display-name: ${quoted(e2eInvitee.displayName)}`
}

function seedTenantSSOAdminAccount(): string {
  return `    - identifier: ${quoted(tenantSSOAdmin.identifier)}
      username: ${quoted(tenantSSOAdmin.username)}
      password: ${quoted(tenantSSOAdmin.password)}
      display-name: ${quoted(tenantSSOAdmin.displayName)}
      platform-admin: true`
}

function seedAccountEntry(
  account: {
    readonly identifier: string
    readonly username?: string
    readonly password: string
    readonly displayName: string
  },
  options: { readonly platformAdmin?: boolean } = {},
): string {
  const lines = [`    - identifier: ${quoted(account.identifier)}`]
  if (account.username !== undefined) {
    lines.push(`      username: ${quoted(account.username)}`)
  }
  lines.push(`      password: ${quoted(account.password)}`)
  lines.push(`      display-name: ${quoted(account.displayName)}`)
  if (options.platformAdmin === true) lines.push('      platform-admin: true')
  return lines.join('\n')
}

interface E2ETenantAdmins {
  readonly name: string
  readonly admins: readonly string[]
  readonly settings?: readonly string[]
}

function defaultTenantAdmins(input: ServerConfigInput): E2ETenantAdmins[] {
  const entries: E2ETenantAdmins[] = [
    { name: 'acme', admins: defaultAcmeAdmins(input) },
  ]
  if (input.ssoProvider !== undefined) {
    const globexAdmins: string[] = [smsRolloutOperator.identifier]
    if (input.tenantSSO === true) globexAdmins.push(tenantSSOAdmin.identifier)
    entries.push({ name: 'globex', admins: globexAdmins })
  }
  return entries
}

function defaultAcmeAdmins(input: ServerConfigInput): string[] {
  const admins: string[] = []
  if (input.seedTenantAdmin === true) admins.push(e2eTenantAdmin.identifier)
  if (input.seedUnprovedTenantAdmin === true) {
    admins.push(e2eUnprovedTenantAdmin.identifier)
  }
  if (admins.length > 0) return admins
  if (input.seedImpersonation === true) {
    return [e2eImpersonationAdmin.identifier]
  }
  return [e2eOrdinaryMember.identifier, smsRolloutOperator.identifier]
}

function e2eTenants(input: ServerConfigInput): E2ETenantAdmins[] {
  const base = defaultTenantAdmins(input)
  const overrides = input.tenantAdmins
  const named = new Set(base.map((entry) => entry.name))
  const out: E2ETenantAdmins[] = base.map((entry) => ({
    name: entry.name,
    admins: overrides?.[entry.name] ?? entry.admins,
  }))
  if (overrides !== undefined) {
    for (const [name, admins] of Object.entries(overrides)) {
      if (!named.has(name)) out.push({ name, admins })
    }
  }
  if (input.tenantRequireMFA === true) {
    const target = out.find((entry) => entry.name === e2eMember.tenant)
    if (target === undefined) {
      throw new Error(
        `the e2e tenant ${e2eMember.tenant} is not in the seeded tenant list, ` +
          'so the tenant-layer require-mfa switch has no layer to land in',
      )
    }
    out[out.indexOf(target)] = {
      ...target,
      settings: [
        ...(target.settings ?? []),
        '- key: "tenant.admission.require-mfa"',
        '  value: "true"',
      ],
    }
  }
  return out
}

function seedTenantEntries(entries: readonly E2ETenantAdmins[]): string {
  return entries
    .map((entry) => {
      const admins = entry.admins.map((admin) => `        - ${admin}`).join('\n')
      const settings =
        entry.settings === undefined || entry.settings.length === 0
          ? ''
          : `      settings:\n${entry.settings
              .map((line) => `        ${line}`)
              .join('\n')}\n`
      return `    - name: ${entry.name}\n      admins:\n${admins}\n${settings}`
    })
    .join('')
}

function replaceOnce(
  body: string,
  needle: string,
  replacement: string,
  label: string,
): string {
  const first = body.indexOf(needle)
  if (first === -1) {
    throw new Error(
      `the configuration template holds no ${label} needle ` +
        `(${JSON.stringify(needle)}); the template and the harness have drifted`,
    )
  }
  if (body.indexOf(needle, first + needle.length) !== -1) {
    throw new Error(
      `the configuration template holds more than one ${label} needle ` +
        `(${JSON.stringify(needle)}); the replacement would be ambiguous`,
    )
  }
  return body.slice(0, first) + replacement + body.slice(first + needle.length)
}

function replaceAll(
  body: string,
  needle: string,
  replacement: string,
  expected: number,
  label: string,
): string {
  const parts = body.split(needle)
  const found = parts.length - 1
  if (found !== expected) {
    throw new Error(
      `the configuration template holds ${found} ${label} needle(s) ` +
        `(${JSON.stringify(needle)}), expected ${expected}`,
    )
  }
  return parts.join(replacement)
}

function quoted(value: string): string {
  return JSON.stringify(value)
}

export async function writeServerConfig(
  input: ServerConfigInput,
): Promise<ServerConfig> {
  const template = await readFile(E2E_CONFIG_TEMPLATE, 'utf8')
  const varDir = join(input.dir, 'var')
  await mkdir(varDir, { recursive: true })

  const backendAddress = `127.0.0.1:${input.backendPort}`
  const origin = `http://localhost:${input.backendPort}`
  const dsn = join(varDir, 'e2e.db')
  const signingKey = join(varDir, 'token-signing-key.pem')
  const objectsRoot = join(varDir, 'objects')
  const path = join(input.dir, 'e2e.yaml')

  let body = template
  body = replaceOnce(
    body,
    'address: "127.0.0.1:8080"',
    `address: ${quoted(backendAddress)}`,
    'backend listener',
  )
  body = replaceOnce(
    body,
    'dsn: "./var/e2e.db"',
    `dsn: ${quoted(dsn)}`,
    'sqlite dsn',
  )
  body = replaceOnce(
    body,
    'signing-key-file: var/token-signing-key.pem',
    `signing-key-file: ${quoted(signingKey)}`,
    'token signing key',
  )
  body = replaceOnce(
    body,
    'root: "./var/objects"',
    `root: ${quoted(objectsRoot)}`,
    'object store root',
  )
  // The template names the published origin in four places (authn public URL, WebAuthn origin, ops document, invitation landing); all four follow this run's browser port.
  body = replaceAll(
    body,
    '"http://localhost:8080"',
    quoted(origin),
    4,
    'public origin',
  )
  if (input.invitationMail === false) {
    // An empty origin is the module's not-configured state: the invitation is still issued and the token stays in the answer. This runs after the origin replacement so the four origins still count.
    body = replaceOnce(
      body,
      `accept-base-url: ${quoted(origin)}`,
      'accept-base-url: ""',
      'invitation mail origin',
    )
  }
  if (input.socialProvider !== undefined) {
    const googleDefaults = `      issuer: "https://accounts.google.com"
      allow-loopback: false
      root-ca-file: ""`
    body = replaceOnce(
      body,
      googleDefaults,
      `      issuer: ${quoted(input.socialProvider.issuer)}
      allow-loopback: true
      root-ca-file: ${quoted(input.socialProvider.rootCAFile)}`,
      'google provider defaults',
    )
  }
  if (input.tenantSSO === true && input.ssoProvider === undefined) {
    throw new Error(
      'the tenant-SSO seed names an enterprise provider the configuration has ' +
        'no seams for; the server fixture must pass ssoProvider with tenantSSO',
    )
  }
  if (input.ssoProvider !== undefined) {
    const ssoDefaults = `    allow-private-egress: false
    dns-resolver: ""
    oidc-ca-file: ""`
    body = replaceOnce(
      body,
      ssoDefaults,
      `    allow-private-egress: true
    dns-resolver: ${quoted(input.ssoProvider.dnsResolver)}
    oidc-ca-file: ${quoted(input.ssoProvider.rootCAFile)}`,
      'enterprise SSO seams',
    )
  }
  body = replaceOnce(
    body,
    '    - name: acme\n      admins:\n        - member\n',
    seedTenantEntries(e2eTenants(input)),
    'seed tenants',
  )
  // The WebAuthn rp-id must stay localhost: the harness opens the browser at http://localhost, and any other value fails every ceremony.
  if (!/^\s*rp-id:\s*localhost\s*$/m.test(body)) {
    throw new Error(
      'the e2e configuration template no longer pins authn.mfa.webauthn.rp-id to localhost; ' +
        'the harness opens the browser at http://localhost:<port>',
    )
  }
  const extraAccounts: string[] = []
  if (input.seedMember === true) extraAccounts.push(seedMemberAccount())
  if (input.seedInvitee === true) extraAccounts.push(seedInviteeAccount())
  if (input.seedSocialAccount === true) {
    extraAccounts.push(seedSocialAccount())
  }
  if (input.tenantSSO === true) {
    extraAccounts.push(seedTenantSSOAdminAccount())
  }
  if (input.seedTenantAdmin === true) {
    extraAccounts.push(seedAccountEntry(e2eTenantAdmin))
  }
  if (input.seedUnprovedTenantAdmin === true) {
    extraAccounts.push(seedAccountEntry(e2eUnprovedTenantAdmin))
  }
  if (input.seedSystemTenantAdmin === true) {
    extraAccounts.push(seedAccountEntry(e2eSystemTenantAdmin))
  }
  if (input.seedImpersonation === true) {
    extraAccounts.push(seedAccountEntry(e2eImpersonationAdmin))
  }
  if (extraAccounts.length > 0) {
    body = replaceOnce(
      body,
      seedOperatorAccount,
      `${seedOperatorAccount}\n${extraAccounts.join('\n')}`,
      'seed operator account',
    )
  }
  if (input.seedSystemTenantAdmin === true) {
    body = replaceOnce(
      body,
      '\n  members:\n',
      `\n  system-admins:\n    - ${e2eSystemTenantAdmin.identifier}\n  members:\n`,
      'seed members section',
    )
  }
  const memberships: string[] = []
  if (input.seedMember === true) memberships.push(seedMemberMembership())
  if (memberships.length > 0) {
    const entries = memberships.join('\n')
    // YAML rejects a duplicate mapping key, so the entries join the template's existing members list instead of opening a second one.
    const settingsNeedle = '\n  settings:\n'
    const membersNeedle = '\n  members:\n'
    const membersAt = body.indexOf(membersNeedle)
    if (membersAt === -1) {
      body = replaceOnce(
        body,
        settingsNeedle,
        `\n  members:\n${entries}\n  settings:\n`,
        'seed settings section',
      )
    } else {
      const rest = body.slice(membersAt + membersNeedle.length)
      const nextKeyAt = rest.search(/^ {2}[a-z-]+:/m)
      if (nextKeyAt === -1) {
        throw new Error(
          'the e2e configuration template ends inside its members list; ' +
            'the harness cannot tell where that list stops',
        )
      }
      const insertAt = membersAt + membersNeedle.length + nextKeyAt
      body = `${body.slice(0, insertAt)}${entries}\n${body.slice(insertAt)}`
    }
  }

  if (input.recentWindow !== undefined) {
    body = replaceOnce(
      body,
      '\n  settings:\n',
      `\n  settings:\n    - key: "authn.mfa.recent-window"\n      value: ${quoted(input.recentWindow)}\n`,
      'recent-window setting',
    )
  }

  if (input.mfaRequired === true) {
    body = replaceOnce(
      body,
      '\n  settings:\n',
      '\n  settings:\n    - key: "authn.mfa.global-required"\n      value: "true"\n',
      'global-required setting',
    )
  }

  if (input.platformRequireMFA === true) {
    body = replaceOnce(
      body,
      '\n  settings:\n',
      '\n  settings:\n    - key: "tenant.admission.require-mfa"\n      value: "true"\n',
      'platform admission floor',
    )
  }

  if (input.identityQuota !== undefined) {
    body = replaceOnce(
      body,
      '\n  settings:\n',
      `\n  settings:\n    - key: "ratelimit.limit"\n      value: ${quoted(input.identityQuota)}\n`,
      'per-identity quota',
    )
  }

  await writeFile(path, body, { encoding: 'utf8', mode: 0o600 })

  return {
    path,
    dir: input.dir,
    backendPort: input.backendPort,
    backendAddress,
    origin,
    dsn,
    signingKey,
    objectsRoot,
  }
}

export const deployOperator = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

const deployValues = {
  publicDomain: 'localhost',
  operatorUsername: deployOperator.username,
  operatorEmail: deployOperator.identifier,
  operatorPassword: deployOperator.password,
  encryptionKey: 'dev-only-change-me-0123456789abcdef',
  smtpHost: '127.0.0.1',
  smtpPort: 2525,
  smtpTls: 'none',
  smtpUsername: '',
  smtpPassword: '',
  smtpFrom: 'demo@demo.test',
  googleClientID: 'e2e-google-client',
  googleClientSecret: 'e2e-google-secret',
  githubClientID: 'e2e-github-client',
  githubClientSecret: 'e2e-github-secret',
  humanverifyMode: 'off',
  humanverifyPhoneMode: 'off',
  turnstileSiteKey: '',
  turnstileSecretKey: '',
  // The entrypoint defaults DEMO_TURNSTILE_ACTION to login; mirror it so a rendered deployment carries the same action.
  turnstileAction: 'login',
} as const

export interface DeployConfigInput {
  readonly volume: string
  readonly backendPort: number
  readonly opsPort: number
}

export interface DeployConfig {
  readonly path: string
  readonly volume: string
  readonly backendPort: number
  readonly opsPort: number
  readonly backendAddress: string
  readonly opsAddress: string
  readonly origin: string
  readonly opsOrigin: string
  readonly dsn: string
  readonly signingKey: string
  readonly objectsRoot: string
}

export async function writeDeployConfig(
  input: DeployConfigInput,
): Promise<DeployConfig> {
  const template = await readFile(DEPLOY_CONFIG_TEMPLATE, 'utf8')
  const backendAddress = `127.0.0.1:${input.backendPort}`
  const opsAddress = `127.0.0.1:${input.opsPort}`
  const origin = `http://localhost:${input.backendPort}`
  const opsOrigin = `http://localhost:${input.opsPort}`
  const dsn = join(input.volume, 'demo.db')
  const signingKey = join(input.volume, 'var', 'token-signing-key.pem')
  const objectsRoot = join(input.volume, 'objects')
  const path = join(input.volume, 'config.yaml')

  let body = template
  body = replaceOnce(
    body,
    'address: "0.0.0.0:8080"',
    `address: ${quoted(backendAddress)}`,
    'backend listener',
  )
  body = replaceOnce(
    body,
    'address: "0.0.0.0:8083"',
    `address: ${quoted(opsAddress)}`,
    'operations listener',
  )
  const replacements: ReadonlyArray<readonly [string, string, number]> = [
    ['__PUBLIC_ORIGIN__', origin, 4],
    ['__PUBLIC_DOMAIN__', deployValues.publicDomain, 2],
    ['__OPERATOR_EMAIL__', deployValues.operatorEmail, 1],
    ['__OPERATOR_USERNAME__', deployValues.operatorUsername, 1],
    ['__OPERATOR_PASSWORD__', deployValues.operatorPassword, 1],
    ['__ENCRYPTION_KEY__', deployValues.encryptionKey, 1],
    ['__SMTP_HOST__', deployValues.smtpHost, 1],
    ['__SMTP_PORT__', String(deployValues.smtpPort), 1],
    ['__SMTP_TLS__', deployValues.smtpTls, 1],
    ['__SMTP_USERNAME__', deployValues.smtpUsername, 1],
    ['__SMTP_PASSWORD__', deployValues.smtpPassword, 1],
    ['__SMTP_FROM__', deployValues.smtpFrom, 1],
    ['__GOOGLE_CLIENT_ID__', deployValues.googleClientID, 1],
    ['__GOOGLE_CLIENT_SECRET__', deployValues.googleClientSecret, 1],
    ['__GITHUB_CLIENT_ID__', deployValues.githubClientID, 1],
    ['__GITHUB_CLIENT_SECRET__', deployValues.githubClientSecret, 1],
    ['__HUMANVERIFY_MODE__', deployValues.humanverifyMode, 1],
    ['__HUMANVERIFY_PHONE_MODE__', deployValues.humanverifyPhoneMode, 1],
    ['__TURNSTILE_SITE_KEY__', deployValues.turnstileSiteKey, 1],
    ['__TURNSTILE_SECRET_KEY__', deployValues.turnstileSecretKey, 1],
    ['__TURNSTILE_ACTION__', deployValues.turnstileAction, 1],
  ]
  for (const [needle, replacement, expected] of replacements) {
    body = replaceAll(body, needle, replacement, expected, `deploy ${needle}`)
  }
  const leftover = body.match(/__[A-Z][A-Z0-9_]*__/)
  if (leftover !== null) {
    throw new Error(
      `the deployment template still carries the placeholder ${leftover[0]} ` +
        'after rendering; the template and the deploy fixture have drifted',
    )
  }

  await writeFile(path, body, { encoding: 'utf8', mode: 0o600 })
  return {
    path,
    volume: input.volume,
    backendPort: input.backendPort,
    opsPort: input.opsPort,
    backendAddress,
    opsAddress,
    origin,
    opsOrigin,
    dsn,
    signingKey,
    objectsRoot,
  }
}
