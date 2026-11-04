/**
 * The review assembly's configuration, one copy per started server.
 *
 * The template under starters/demo/server/review is the assembly's own; this
 * module copies it into the server's temporary directory and replaces every
 * value that names a shared resource -- the listener address, the database,
 * the token signing key, the object store's root, and the origin the browser
 * and WebAuthn trust. One listener carries the API, the frontend and the
 * operations probes, so the browser origin is also where the fixture answers
 * its ops probes. A needle that is not in the template (or is there the
 * wrong number of times) fails the fixture: a run over a half-substituted
 * template would bind the published port or write into the source tree, and
 * that must be a loud harness error, never a silent fallback.
 *
 * The remaining values are deliberately kept: the seeded operator, the open
 * signup settings, the SMS prefixes and the review carriers are what make the
 * journeys possible. The template's social credentials are fake; when the
 * server fixture starts the local identity provider it passes `socialProvider`
 * and the Google block gains the issuer, the loopback switch and the CA file
 * that point the method at it. Without that input the template's public
 * defaults stand. The credentials in the template reach no provider either way.
 *
 * The enterprise SSO seams work the same way. The template leaves the loopback
 * relaxation off and both outbound seams empty -- the deployment defaults: the
 * system resolver and the system roots. When the server fixture starts the
 * enterprise journey's local fixtures it passes `ssoProvider`, and this file
 * points all three at them: the DNS stub's resolver address, the CA file the
 * enterprise provider minted, and the loopback switch the restricted egress
 * client needs to reach that provider. Without that input the template stands,
 * and a deployment keeps it as it is.
 *
 * The enterprise journey also switches organizations at its end, which needs a
 * second organization the employee already belongs to. The template seeds one
 * tenant (acme); the same opt-in registers a second one (globex) through the
 * binary's seed command. Every seeded tenant names at least one administrator,
 * and an administrator is a member of the tenant it administers, so globex
 * names the platform operator -- the account the seed already carries and the
 * one that drives the admin. The employee's memberships themselves are
 * product writes: the spec adds them through the admin's member page, never
 * through this file.
 *
 * The administrators a tenant starts with are written from one explicit list
 * (reviewTenants), never by appending to the template's acme entry. A journey
 * that needs a tenant with exactly one tenant-admin binding names that
 * binding through `tenantAdmins` or one of the tenant-admin opt-ins; the
 * impersonation server names its own administrator because the account it
 * impersonates must be an ordinary member.
 */
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import {
  enterpriseIdentityProviderUser,
  identityProviderUser,
} from './identity-provider'
import { DEPLOY_CONFIG_TEMPLATE, REVIEW_CONFIG_TEMPLATE } from './paths'

export interface ReviewConfigInput {
  /** The server's own temporary directory; the config and state live there. */
  readonly dir: string
  /** The backend listener's port (the browser's origin, the API and the ops probes). */
  readonly backendPort: number
  /**
   * Seed the member-workspace journey's account and membership. Off by
   * default: only that journey's server reads them, so the shared template's
   * seed -- the operator, the tenant and the settings -- stays the baseline
   * every other spec starts from.
   */
  readonly seedMember?: boolean
  /**
   * Seed the impersonation journey's two second factors on the operator. Off by
   * default: those factors make every later sign-in of that operator complete a
   * second factor, so only the journey that needs them turns it on.
   */
  readonly seedImpersonation?: boolean
  /**
   * Seed the invitation journey's invitee: an enabled user account whose login
   * name is the address the operator invites, and which holds no membership.
   * Off by default: only that journey signs the account in, and the account's
   * address is proved by the acceptance, so every other server starts from the
   * template's own seed.
   */
  readonly seedInvitee?: boolean
  /**
   * Seed an enabled account whose email login name is the fixed identity
   * provider user's address. Off by default: only the social journey's branch
   * where the provider-verified address already belongs to an account turns it
   * on, so every other server starts from the template's own seed.
   */
  readonly seedSocialAccount?: boolean
  /**
   * Point the Google method at a provider other than the one its defaults
   * name. The end-to-end harness starts a local OpenID Connect provider for a
   * spec that opts in and passes its issuer and CA file here. An absent value
   * leaves the template's defaults, which are the public provider.
   */
  readonly socialProvider?: {
    readonly issuer: string
    readonly rootCAFile: string
  }
  /**
   * Point the enterprise SSO seams at the local fixtures. The end-to-end
   * harness starts a second OpenID Connect provider and a UDP DNS stub for a
   * spec that opts in, and passes the stub's resolver address and the CA file
   * the provider minted here. An absent value leaves the template's empty
   * values and the relaxation off, which is the deployment default: the TXT
   * lookup takes the system resolver and the enterprise OIDC egress trusts the
   * system roots alone.
   */
  readonly ssoProvider?: {
    readonly dnsResolver: string
    readonly rootCAFile: string
  }
  /**
   * Lower `authn.mfa.recent-window` to this duration in the seed settings.
   * Off by default: the template keeps the declared window, and only a
   * journey that must observe the window passing asks for a shorter one. The
   * value is a deployment setting the binary's own seed writes -- assembly,
   * not a manufactured product state.
   */
  readonly recentWindow?: string
  /**
   * Require every account to register a second factor, so an account with none
   * is asked for one at its next login. Off by default: the deployment's own
   * switch stays as the template declares it, and only a journey that needs a
   * factor registered before its session asks for the requirement. It is a
   * deployment setting the binary's own seed writes -- assembly, not a
   * manufactured product state.
   */
  readonly mfaRequired?: boolean
  /**
   * Replace the scheduler's tick with this duration. Off by default: the
   * template's two-second tick feeds the member-workspace journey's worker. A
   * journey whose dispatch is a control click and whose only intended sends are
   * its own turns the periodic trigger off with `0s`, which the scheduler
   * treats as no trigger at all; the job queue that carries the click's
   * dispatch polls independently.
   */
  readonly schedulerTick?: string
  /**
   * Seed the tenant-SSO journey's state through the binary's own `seed` command:
   * the enterprise fixture's fixed user as the target tenant's administrator.
   * Off by default: the account and its administrator binding exist for the
   * journey alone. The provider and the DNS stub the journey needs come from
   * `ssoProvider`; this input contributes the account and the administrator
   * binding, never a database row written around the product. The tenant's SSO
   * requirement is not seeded: the journey enables it through the admin's
   * settings surface once its administrator's admin session is established,
   * because the admin's own permission read is judged by the admission layer
   * and a requirement already on would keep the page from being opened.
   */
  readonly tenantSSO?: boolean
  /**
   * Seed the tenant-admin journey's proved-email business administrator and
   * make it acme's only tenant administrator. Off by default: the account owns
   * a mailbox the notification fixture would address, and only the journey that
   * drives the workspace admin surface needs it. The account is added through
   * the binary's own seed, with the review build proving its address.
   */
  readonly seedTenantAdmin?: boolean
  /**
   * Seed the tenant-admin journey's unproved-email business administrator and
   * make it acme's administrator. Off by default. It is what the journey signs
   * in as on its refused-write branch; the seed asserts the address without
   * proving it, which is the state the write gate refuses.
   */
  readonly seedUnprovedTenantAdmin?: boolean
  /**
   * Seed a system-tenant administrator outside the platform-administrator
   * role, under the seed's `system-admins` list. Off by default: only the
   * journey that proves the system tenant is not manageable through the
   * workspace admin surface needs it.
   */
  readonly seedSystemTenantAdmin?: boolean
  /**
   * Replace the administrators each seeded tenant starts with, keyed by tenant
   * name. An absent key keeps the tenant's default administrator list, and a
   * key naming a tenant the default list does not carry adds that tenant. This
   * is what lets a journey give a tenant exactly one `tenant-admin` binding
   * instead of the two the default acme list carries.
   */
  readonly tenantAdmins?: Readonly<Record<string, readonly string[]>>
}

export interface ReviewConfig {
  /** The configuration file's absolute path. */
  readonly path: string
  /** The server's working directory. */
  readonly dir: string
  readonly backendPort: number
  readonly backendAddress: string
  /** The browser-facing origin, e.g. http://localhost:41234. */
  readonly origin: string
  readonly dsn: string
  readonly signingKey: string
  readonly objectsRoot: string
}

/**
 * The member the member-workspace journey signs in as.
 *
 * The review template's seed holds the platform operator alone, and a platform
 * administrator's role binding is not a membership: the seeded tenant has no
 * member for the notification worker to address. When the member-workspace
 * spec asks for it (`seedMember`), this account and its membership are added
 * to the configuration the harness writes, so the binary's own `seed` command
 * creates both -- the fixture never writes a row into a running database. The
 * account is seeded by an email address on purpose: the review type's default
 * channels include email and sms, the account holds no phone number, and the
 * worker skips a member with neither a proved phone nor an email identifier.
 */
export const reviewMember = {
  tenant: 'acme',
  identifier: 'member@demo.test',
  username: 'member-review',
  password: 'member-review-secret',
  displayName: 'Demo Member',
} as const

/**
 * The platform operator the review template seeds, and the tenant the
 * SMS-template rollout journey drives it in.
 *
 * The template's operator is a platform administrator and deliberately not a
 * member of the seeded tenant, because the impersonation journey targets that
 * tenant from outside and its initiator holds no permission there. Every server
 * that does not opt into the tenant-admin or impersonation fixture names this
 * account acme's second administrator, and an administrator is a member of the
 * tenant it administers; see reviewTenants. The account and its credential are
 * the template's own.
 */
export const smsRolloutOperator = {
  tenant: 'acme',
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/**
 * The account the invitation journey invites and then signs in as.
 *
 * It is seeded by an email address on purpose: the invitation's acceptance
 * looks the invited address up and hangs the membership on the account that
 * holds it, and the acceptance is the write that proves the address (the seed
 * writes it as an administrator's assertion, which is not proof of ownership).
 * The account holds no membership, so the acceptance is what makes it a member
 * of the invited tenant. Its password is a review fixture, not a credential.
 */
export const reviewInvitee = {
  identifier: 'invitee@demo.test',
  username: 'invitee-review',
  password: 'invitee-review-secret',
  displayName: 'Invitation Invitee',
} as const

/**
 * The account the social journey's alternative branch finds holding the
 * provider's address.
 *
 * It is seeded through the binary's own seed command, as an email login name
 * the seed writes with an administrator's assertion (not a provider's proof).
 * That is exactly the precondition the branch needs: the address exists on an
 * account, automatic linking is off, so a social login with the same verified
 * address is refused with the link-required refusal rather than taking the
 * account over. Its password is a review value, not a credential.
 */
export const reviewSocialAccount = {
  identifier: identityProviderUser.email,
  username: 'social-signin',
  password: 'social-review-secret',
  displayName: identityProviderUser.name,
} as const

/**
 * The tenant that requires enterprise SSO in the tenant-SSO journey. It is the
 * second tenant the enterprise fixture's opt-in registers, and the journey's
 * administrator belongs to it.
 */
export const tenantSSOTenant = 'globex'

/**
 * The tenant administrator the tenant-SSO journey signs in as.
 *
 * The address is the enterprise fixture's fixed user's, so the provider's
 * pre-provisioned match lands on this account; the password gives the journey
 * the local-first-factor session it starts from. The display name deliberately
 * differs from the provider's `name` claim, so an account opened from the
 * provider's claims would show the claim and fail the same-account assertion.
 * The account is also a platform administrator because this host's admin --
 * where the invitations-and-members page lives -- is platform-staff-only; the
 * tenant-administrator binding in `tenantSSOTenant` is what the journey's
 * invitations exercise.
 */
export const tenantSSOAdmin = {
  identifier: enterpriseIdentityProviderUser.email,
  username: 'globex-admin',
  password: 'tenant-sso-review-secret',
  displayName: 'Globex Administrator',
} as const

/**
 * The business tenant administrator the tenant-admin journey signs in as.
 *
 * It is acme's only tenant administrator and holds no platform permission, so
 * it reaches the workspace admin surface with the governed set alone. Its
 * address is proved by the review seed, because an unproved address is refused
 * every workspace admin write; the username is its login name. The account
 * also owns the personal tenant every account gets at creation, which is the
 * tenant the journey switches to and back from. A review value, not a
 * credential.
 */
export const reviewTenantAdmin = {
  tenant: 'acme',
  identifier: 'tenant-admin@demo.test',
  username: 'tenant-admin-review',
  password: 'tenant-admin-review-secret',
  displayName: 'Tenant Administrator',
} as const

/**
 * The tenant-admin journey's second business administrator, whose address the
 * seed asserts but does not prove. The journey drives the refused writes an
 * unproved address causes and then proves the address in the account page; the
 * seed never proves it, so the starting state is the one that gate describes.
 */
export const reviewUnprovedTenantAdmin = {
  tenant: 'acme',
  identifier: 'unproved-admin@demo.test',
  username: 'unproved-admin-review',
  password: 'unproved-admin-review-secret',
  displayName: 'Unproved Tenant Administrator',
} as const

/**
 * The system tenant's own administrator.
 *
 * It is seeded under `seed.system-admins`, so it holds the system tenant's
 * tenant-admin role without the platform-administrator role. The platform
 * console lists the account like any other, so the operator can see it there
 * while the journey proves the system tenant is not manageable through the
 * workspace admin surface. It is a username, so it signs in with a login name
 * and needs no mailbox.
 */
export const reviewSystemTenantAdmin = {
  tenant: 'system',
  identifier: 'system-admin-review',
  password: 'system-admin-review-secret',
  displayName: 'System Tenant Administrator',
} as const

/**
 * The impersonation journey's target: an ordinary member of acme that holds no
 * role binding, so it covers no workspace admin page. It is the template's own
 * `member` account, seeded without a mailbox on purpose: the notification
 * fixture addresses every member with one, and an extra destination in acme
 * would change the delivery counts another fixture asserts.
 */
export const reviewOrdinaryMember = {
  tenant: 'acme',
  identifier: 'member',
  password: 'member-dev-secret',
  displayName: 'Demo Member',
} as const

/**
 * The administrator acme keeps on the impersonation journey's server.
 *
 * The impersonation requirement is an initiator outside the target tenant, so
 * the platform operator cannot be acme's administrator there; the tenant still
 * needs one, and the ordinary member the journey impersonates must not be it.
 * The account is a username with no mailbox, which keeps the notification
 * fixture's delivery counts where they were.
 */
export const reviewImpersonationAdmin = {
  tenant: 'acme',
  identifier: 'acme-owner',
  password: 'acme-owner-secret',
  displayName: 'Acme Owner',
} as const

/**
 * A declared setting the tenant-admin journey writes on its tenant's layer.
 *
 * The namespace is the module that declares it and is outside the platform-only
 * set (`ratelimit.*`, `tenant.admission.*`, `authn.*`), so the workspace admin
 * settings page lists it as an editable row. `notification.delivery` is part of
 * the review assembly, so its budget keys are declared on every review server.
 * The value stays inside the declared domain of whole numbers from one to a
 * thousand.
 */
export const reviewTenantSetting = {
  namespace: 'notification.delivery',
  key: 'notification.delivery.per-recipient-hourly',
  /** The declared default the row resolves to before the tenant writes it. */
  defaultValue: '20',
  /** The value the journey writes on the tenant layer. */
  value: '25',
} as const

/** The template's seeded operator entry; the member entry is appended to it. */
const seedOperatorAccount = `    - identifier: operator@demo.test
      username: operator
      password: operator-dev-secret
      display-name: Demo Operator
      platform-admin: true`

/** The member's `seed.accounts` entry, indented for the list it joins. */
function seedMemberAccount(): string {
  return `    - identifier: ${quoted(reviewMember.identifier)}
      username: ${quoted(reviewMember.username)}
      password: ${quoted(reviewMember.password)}
      display-name: ${quoted(reviewMember.displayName)}`
}

/** The member's `seed.members` entry, indented for the key it joins. */
function seedMemberMembership(): string {
  return `    - tenant: ${quoted(reviewMember.tenant)}
      account: ${quoted(reviewMember.identifier)}`
}

/** The social account's `seed.accounts` entry, indented for the list it joins. */
function seedSocialAccount(): string {
  return `    - identifier: ${quoted(reviewSocialAccount.identifier)}
      username: ${quoted(reviewSocialAccount.username)}
      password: ${quoted(reviewSocialAccount.password)}
      display-name: ${quoted(reviewSocialAccount.displayName)}`
}

/** The invitee's `seed.accounts` entry, indented for the list it joins. */
function seedInviteeAccount(): string {
  return `    - identifier: ${quoted(reviewInvitee.identifier)}
      username: ${quoted(reviewInvitee.username)}
      password: ${quoted(reviewInvitee.password)}
      display-name: ${quoted(reviewInvitee.displayName)}`
}

/**
 * The tenant-SSO administrator's `seed.accounts` entry. It joins the operator's
 * list, and it is marked `platform-admin` because the admin is platform-staff
 * only; the tenant-admin binding comes from the tenant entry below.
 */
function seedTenantSSOAdminAccount(): string {
  return `    - identifier: ${quoted(tenantSSOAdmin.identifier)}
      username: ${quoted(tenantSSOAdmin.username)}
      password: ${quoted(tenantSSOAdmin.password)}
      display-name: ${quoted(tenantSSOAdmin.displayName)}
      platform-admin: true`
}

/**
 * One `seed.accounts` entry rendered for the list it joins. The username is
 * written beside an email identifier, which the seed requires; `proved-email`
 * asks the review build to prove the address, and `platform-admin` marks the
 * account for the platform role. Both flags are emitted only when set, so an
 * ordinary entry keeps the template's shape.
 */
function seedAccountEntry(
  account: {
    readonly identifier: string
    readonly username?: string
    readonly password: string
    readonly displayName: string
  },
  options: { readonly provedEmail?: boolean; readonly platformAdmin?: boolean } = {},
): string {
  const lines = [`    - identifier: ${quoted(account.identifier)}`]
  if (account.username !== undefined) {
    lines.push(`      username: ${quoted(account.username)}`)
  }
  lines.push(`      password: ${quoted(account.password)}`)
  lines.push(`      display-name: ${quoted(account.displayName)}`)
  if (options.provedEmail === true) lines.push('      proved-email: true')
  if (options.platformAdmin === true) lines.push('      platform-admin: true')
  return lines.join('\n')
}

/** One seeded tenant and its explicit administrator list. */
interface ReviewTenantAdmins {
  readonly name: string
  readonly admins: readonly string[]
}

/**
 * The administrators a tenant starts with when the caller names none.
 *
 * The tenant-admin flags win over the impersonation default because the journey
 * that sets them makes its account the tenant's administrator. The impersonation
 * server demotes the ordinary member and names its own administrator, because
 * the impersonated account must cover no workspace admin page and the operator
 * is not a member of the tenant. Every other server keeps the template's member
 * as an administrator and adds the platform operator, which is the tenant-domain
 * grant the console's tenant-addressed pages decide on.
 */
function defaultTenantAdmins(input: ReviewConfigInput): ReviewTenantAdmins[] {
  const entries: ReviewTenantAdmins[] = [
    { name: 'acme', admins: defaultAcmeAdmins(input) },
  ]
  if (input.ssoProvider !== undefined) {
    const globexAdmins: string[] = [smsRolloutOperator.identifier]
    if (input.tenantSSO === true) globexAdmins.push(tenantSSOAdmin.identifier)
    entries.push({ name: 'globex', admins: globexAdmins })
  }
  return entries
}

/** acme's default administrator list; see defaultTenantAdmins. */
function defaultAcmeAdmins(input: ReviewConfigInput): string[] {
  const admins: string[] = []
  if (input.seedTenantAdmin === true) admins.push(reviewTenantAdmin.identifier)
  if (input.seedUnprovedTenantAdmin === true) {
    admins.push(reviewUnprovedTenantAdmin.identifier)
  }
  if (admins.length > 0) return admins
  if (input.seedImpersonation === true) {
    return [reviewImpersonationAdmin.identifier]
  }
  return [reviewOrdinaryMember.identifier, smsRolloutOperator.identifier]
}

/**
 * The tenants the seed registers, in order. The per-tenant override replaces
 * one tenant's administrator list and keeps every other default, so a journey
 * that only pins acme's administrator does not drop the enterprise journey's
 * second organization by accident.
 */
function reviewTenants(input: ReviewConfigInput): ReviewTenantAdmins[] {
  const base = defaultTenantAdmins(input)
  if (input.tenantAdmins === undefined) return base
  const named = new Set(base.map((entry) => entry.name))
  const out: ReviewTenantAdmins[] = base.map((entry) => ({
    name: entry.name,
    admins: input.tenantAdmins?.[entry.name] ?? entry.admins,
  }))
  for (const [name, admins] of Object.entries(input.tenantAdmins)) {
    if (!named.has(name)) out.push({ name, admins })
  }
  return out
}

/** Render the tenants and their administrators as the template's YAML list. */
function seedTenantEntries(entries: readonly ReviewTenantAdmins[]): string {
  return entries
    .map((entry) => {
      const admins = entry.admins.map((admin) => `        - ${admin}`).join('\n')
      return `    - name: ${entry.name}\n      admins:\n${admins}\n`
    })
    .join('')
}

/** Replace exactly one occurrence of a needle, or fail the fixture. */
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

/** Replace every occurrence of a needle, requiring the expected count. */
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

/** Quote a value for the YAML template. */
function quoted(value: string): string {
  return JSON.stringify(value)
}

/**
 * Write a per-server configuration and answer with the values the fixture and
 * the tests need. The template is read on every call so a server never shares
 * a file with another.
 */
export async function writeReviewConfig(
  input: ReviewConfigInput,
): Promise<ReviewConfig> {
  const template = await readFile(REVIEW_CONFIG_TEMPLATE, 'utf8')
  const varDir = join(input.dir, 'var')
  await mkdir(varDir, { recursive: true })

  const backendAddress = `127.0.0.1:${input.backendPort}`
  const origin = `http://localhost:${input.backendPort}`
  const dsn = join(varDir, 'review.db')
  const signingKey = join(varDir, 'token-signing-key.pem')
  const objectsRoot = join(varDir, 'objects')
  const path = join(input.dir, 'review.yaml')

  let body = template
  body = replaceOnce(
    body,
    'address: "127.0.0.1:8080"',
    `address: ${quoted(backendAddress)}`,
    'backend listener',
  )
  body = replaceOnce(
    body,
    'dsn: "./var/review.db"',
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
  // The template names the published origin in three places: the public URL
  // the authn gate advertises, the WebAuthn origin, and the ops document's
  // server list. All three follow the port the browser actually opens, which
  // is also where the operations probes answer now that one listener carries
  // both surfaces.
  body = replaceAll(
    body,
    '"http://localhost:8080"',
    quoted(origin),
    3,
    'public origin',
  )
  // The social journey drives a real round trip through a provider the harness
  // starts on loopback. The Google block's client items are the template's
  // fake review credentials, and its endpoint items are the method's public
  // defaults; the three below are what point the method at the local provider.
  // The loopback switch is the seam's own, and the CA file is the one the
  // provider minted, so the restricted egress client verifies the provider's
  // certificate against the system roots plus that file. A template that lost
  // one of the defaults fails here rather than silently keeping the public
  // provider while the journey expects a callback.
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
  // The enterprise journey's fixtures are a separate opt-in with their own
  // seams. The template states the deployment defaults -- the loopback
  // relaxation off and both seams empty -- and the three below replace the
  // block whole when the server fixture starts the local provider and the DNS
  // stub: the stub's resolver address answers the test domain's TXT name, the
  // CA file signed the provider's certificate, and the restricted egress
  // client needs the relaxation to reach the provider on loopback. A template
  // that lost the block fails here rather than silently leaving the system
  // resolver and roots in place while the journey expects the fixtures.
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
    // The enterprise onboarding journey's last step moves the employee from
    // the organization they already belong to into the tenant the provider was
    // registered in. That second organization is part of the deployment's
    // start state: the seed registers it with the platform operator as its
    // administrator, exactly as it does for the template's own tenant. The
    // employee's membership in either organization is written through the
    // admin by the spec, so nothing here manufactures the employee's
    // membership.
    //
    // The tenant-SSO journey's second tenant has a different shape: its
    // administrators are the platform operator, who reaches the management
    // surface to register the provider, and the enterprise fixture's own user,
    // whom the provider's pre-provisioned match lands on, so the session keeps
    // the same account and administrator binding across the re-login. The
    // admin's settings surface switches the tenant's SSO requirement on once
    // the administrator's session is established; the seed writes neither
    // layer's row. The provider and invitation routes are tenant-resolved on
    // the API endpoint, and the admission layer judges a request in the
    // organization the request surface announces, so the spec moves the tenant
    // area to globex before the writes the requirement holds to.
  }
  // The seeded tenants and their administrators are written from one explicit
  // list, never by appending to the template's acme entry. The impersonation
  // server and the tenant-admin journey both need a tenant whose administrator
  // is not the template's member, and a per-tenant override is what lets a
  // journey give a tenant exactly one tenant-admin binding. The enterprise
  // opt-ins add their second organization to the same list.
  body = replaceOnce(
    body,
    '    - name: acme\n      admins:\n        - member\n',
    seedTenantEntries(reviewTenants(input)),
    'seed tenants',
  )
  // The WebAuthn relying party is the browser's host. The review template
  // pins it to localhost; a run that changed it would fail every ceremony with
  // an origin mismatch, so its absence is a template drift.
  if (!/^\s*rp-id:\s*localhost\s*$/m.test(body)) {
    throw new Error(
      'the review configuration template no longer pins authn.mfa.webauthn.rp-id to localhost; ' +
        'the harness opens the browser at http://localhost:<port>',
    )
  }
  // The accounts a journey signs in as, beyond the seeded operator: their
  // entries join the operator's list in the same `seed.accounts` item. They are
  // part of the configuration the binary's own seed command reads, so the
  // accounts are the product's writes, not the fixture's. Each block is opt-in
  // so a spec that does not sign the account in starts from the template's own
  // seed.
  const extraAccounts: string[] = []
  if (input.seedMember === true) extraAccounts.push(seedMemberAccount())
  if (input.seedInvitee === true) extraAccounts.push(seedInviteeAccount())
  if (input.seedSocialAccount === true) {
    extraAccounts.push(seedSocialAccount())
  }
  if (input.tenantSSO === true) {
    extraAccounts.push(seedTenantSSOAdminAccount())
  }
  // The tenant-admin journey's accounts and the impersonation server's
  // administrator. Each is opt-in: the tenant-admin accounts own mailboxes the
  // notification fixture would address, and the impersonation administrator is
  // an extra account the other two servers using that opt-in must count.
  if (input.seedTenantAdmin === true) {
    extraAccounts.push(seedAccountEntry(reviewTenantAdmin, { provedEmail: true }))
  }
  if (input.seedUnprovedTenantAdmin === true) {
    extraAccounts.push(seedAccountEntry(reviewUnprovedTenantAdmin))
  }
  if (input.seedSystemTenantAdmin === true) {
    extraAccounts.push(seedAccountEntry(reviewSystemTenantAdmin))
  }
  if (input.seedImpersonation === true) {
    extraAccounts.push(seedAccountEntry(reviewImpersonationAdmin))
  }
  if (extraAccounts.length > 0) {
    body = replaceOnce(
      body,
      seedOperatorAccount,
      `${seedOperatorAccount}\n${extraAccounts.join('\n')}`,
      'seed operator account',
    )
  }
  // The system tenant is registered by the seed itself and must not be listed
  // under seed.tenants, so an account that administers it outside the platform
  // role is named by `seed.system-admins`. The seed writes the membership and
  // the tenant-admin binding through its own seams; the platform console lists
  // the account like any other.
  if (input.seedSystemTenantAdmin === true) {
    body = replaceOnce(
      body,
      '\n  members:\n',
      `\n  system-admins:\n    - ${reviewSystemTenantAdmin.identifier}\n  members:\n`,
      'seed members section',
    )
  }
  // An account a journey signs in as, when that journey also needs it to be a
  // member, gets a membership row appended to the seed's members. The rows go
  // before `settings` so the key order the template documents is kept. The
  // invitee deliberately gets none: the acceptance is what makes it a member.
  const memberships: string[] = []
  if (input.seedMember === true) memberships.push(seedMemberMembership())
  if (memberships.length > 0) {
    const entries = memberships.join('\n')
    // The review template may already carry a members list (the impersonation
    // fixture seeds its target there). YAML rejects a mapping with the same key
    // twice, so the entries join that existing list rather than opening a
    // second key: they are inserted at the end of the list, which is the line
    // before the next two-space key after it.
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
          'the review configuration template ends inside its members list; ' +
            'the harness cannot tell where that list stops',
        )
      }
      const insertAt = membersAt + membersNeedle.length + nextKeyAt
      body = `${body.slice(0, insertAt)}${entries}\n${body.slice(insertAt)}`
    }
  }

  // A journey whose dispatch is its own click turns the periodic scheduler off:
  // the template's tick feeds the member-workspace worker, and a worker that
  // addressed the same tenant would send the rollout journey's notice on its own
  // windows. A tick at or below zero starts no trigger at all; the job queue
  // that carries the click's dispatch polls on its own interval.

  if (input.schedulerTick !== undefined) {
    body = replaceOnce(
      body,
      'tick: 2s',
      `tick: ${quoted(input.schedulerTick)}`,
      'scheduler tick',
    )
  }

  // The impersonation fixture's second factors change how the seeded operator
  // signs in: every later sign-in must complete one. Only the journey that
  // needs it turns it on; every other server starts from the template's own
  // seed with the factors off. The tenant's administrator list is decided by
  // reviewTenants, not here: the impersonation server keeps the operator outside
  // acme and names its own administrator, while every other server names the
  // operator acme's second administrator.
  if (input.seedImpersonation !== true) {
    body = replaceOnce(
      body,
      '\n  impersonation: true\n',
      '\n  impersonation: false\n',
      'seed impersonation switch',
    )
  }

  // A journey that has to observe a self-service freshness window passing
  // lowers it to the setting's domain floor in the seed settings. The binary's
  // seed writes it like any other setting, so the spec still waits out a real
  // window rather than manufacturing a stale session.
  if (input.recentWindow !== undefined) {
    body = replaceOnce(
      body,
      '\n  settings:\n',
      `\n  settings:\n    - key: "authn.mfa.recent-window"\n      value: ${quoted(input.recentWindow)}\n`,
      'recent-window setting',
    )
  }

  // A journey whose session must post-date the factor it enrolled needs the
  // deployment to require a factor, so the login flow registers it before the
  // session exists. The binary's own seed writes the switch like any other
  // setting; the journey still drives registration through the rendered flow.
  if (input.mfaRequired === true) {
    body = replaceOnce(
      body,
      '\n  settings:\n',
      '\n  settings:\n    - key: "authn.mfa.global-required"\n      value: "true"\n',
      'global-required setting',
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

/**
 * The operator the deployment's seed writes.
 *
 * The deploy template seeds this account from the rendered operator secrets.
 * The fixture renders the same values so the browser can sign in as the
 * account the seed wrote; they are review values, not credentials that reach
 * a provider. The entry names an address and a username: the seed writes the
 * address as an administrator's assertion it does not prove, and the username
 * as the login name. The browser signs in with the username, because the
 * unproved address is not a login way; the unproved address is what the deploy
 * journey's refusal step needs.
 */
export const deployOperator = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/**
 * The values the deploy fixture renders into the deployment template. The
 * template's placeholders are filled by the container entrypoint from Fly
 * secrets; the fixture fills them with review values. The SMTP relay, the OAuth
 * applications and the encryption key are fake and reach nothing: this journey
 * sends no mail and completes no social round trip.
 */
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
  googleClientID: 'review-google-client',
  googleClientSecret: 'review-google-secret',
  githubClientID: 'review-github-client',
  githubClientSecret: 'review-github-secret',
  // The deploy journey sets both human-verification surfaces off explicitly;
  // the entrypoint defaults the non-phone surface to pow and the phone surface
  // to captcha. The Turnstile values are rendered empty because the entrypoint
  // writes them only when a selected surface is turnstile.
  humanverifyMode: 'off',
  humanverifyPhoneMode: 'off',
  turnstileSiteKey: '',
  turnstileSecretKey: '',
} as const

export interface DeployConfigInput {
  /** The temporary volume the rendered configuration and state live on. */
  readonly volume: string
  /** The backend listener's port (the browser's origin, and the API). */
  readonly backendPort: number
  /** The operations listener's port. */
  readonly opsPort: number
}

/** A rendered deployment configuration and the paths it names. */
export interface DeployConfig {
  /** The rendered configuration's absolute path on the volume. */
  readonly path: string
  /** The temporary volume; every relative path in the document resolves here. */
  readonly volume: string
  readonly backendPort: number
  readonly opsPort: number
  readonly backendAddress: string
  readonly opsAddress: string
  /** The browser-facing origin, e.g. http://localhost:41234. */
  readonly origin: string
  /** The operations listener's origin. */
  readonly opsOrigin: string
  readonly dsn: string
  readonly signingKey: string
  readonly objectsRoot: string
}

/**
 * Render the deployment template onto the volume, the way
 * deploy/entrypoint.sh does, and answer with the values the fixture and the
 * tests read.
 *
 * The placeholders sit inside double-quoted YAML scalars (except the SMTP
 * port), so each is replaced with the raw value rather than a quoted one. The
 * two listener addresses are the deployment template's own published ones; the
 * fixture substitutes the loopback addresses on its free ports so a run never
 * binds 8080/8083. A placeholder that is absent, duplicated, or left behind
 * after rendering fails the fixture: a half-rendered document would start a
 * server with a placeholder origin.
 */
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
  // The template names the public origin in five places: the API's CORS
  // origins, the operations document's server list, the authentication gate's
  // public URL, a comment naming the registered callback addresses, and the
  // WebAuthn origins. The entrypoint replaces every line, comments included;
  // all five follow the browser's origin.
  const replacements: ReadonlyArray<readonly [string, string, number]> = [
    ['__PUBLIC_ORIGIN__', origin, 5],
    // The public domain appears twice: the WebAuthn relying party id and the
    // hostname the human-verification widget must have run on.
    ['__PUBLIC_DOMAIN__', deployValues.publicDomain, 2],
    // The operator's address is the account's identifier and the administrator
    // the template names for the seeded tenant.
    ['__OPERATOR_EMAIL__', deployValues.operatorEmail, 1],
    // The username is the name the password login accepts.
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
