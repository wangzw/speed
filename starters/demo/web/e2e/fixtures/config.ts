/**
 * The review assembly's configuration, one copy per started server.
 *
 * The template under starters/demo/server/review is the assembly's own; this
 * module copies it into the server's temporary directory and replaces every
 * value that names a shared resource -- both listener addresses, the database,
 * the token signing key, the object store's root, and the origin the browser
 * and WebAuthn trust. A needle that is not in the template (or is there the
 * wrong number of times) fails the fixture: a run over a half-substituted
 * template would bind the published ports or write into the source tree, and
 * that must be a loud harness error, never a silent fallback.
 *
 * The remaining values are deliberately kept: the seeded operator, the open
 * signup settings, the SMS prefixes and the review carriers are what make the
 * journeys possible. The credentials in the template are fake and reach no
 * provider.
 */
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import { REVIEW_CONFIG_TEMPLATE } from './paths'

export interface ReviewConfigInput {
  /** The server's own temporary directory; the config and state live there. */
  readonly dir: string
  /** The backend listener's port (the browser's origin, and the API). */
  readonly backendPort: number
  /** The operations listener's port. */
  readonly opsPort: number
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
}

export interface ReviewConfig {
  /** The configuration file's absolute path. */
  readonly path: string
  /** The server's working directory. */
  readonly dir: string
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
 * The member the member-workspace journey signs in as.
 *
 * The review template's seed holds the platform operator alone, and a platform
 * administrator's role binding is not a membership: the seeded tenant has no
 * member for the notification worker to address. When the member-workspace
 * spec asks for it (`seedMember`), this account and its membership are added
 * to the configuration the harness writes, so the binary's own `seed` command
 * creates both -- the fixture never writes a row into a running database. The
 * account is seeded by an email address on purpose: the review type's default
 * channels include email, and the worker skips a member with no email
 * identifier.
 */
export const reviewMember = {
  tenant: 'acme',
  identifier: 'member@demo.test',
  password: 'member-review-secret',
  displayName: 'Demo Member',
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
  password: 'invitee-review-secret',
  displayName: 'Invitation Invitee',
} as const

/** The template's seeded operator entry; the member entry is appended to it. */
const seedOperatorAccount = `    - identifier: operator@demo.test
      password: operator-dev-secret
      display-name: Demo Operator
      platform-admin: true`

/** The member's `seed.accounts` entry, indented for the list it joins. */
function seedMemberAccount(): string {
  return `    - identifier: ${quoted(reviewMember.identifier)}
      password: ${quoted(reviewMember.password)}
      display-name: ${quoted(reviewMember.displayName)}`
}

/** The member's `seed.members` entry, indented for the key it joins. */
function seedMemberMembership(): string {
  return `    - tenant: ${quoted(reviewMember.tenant)}
      account: ${quoted(reviewMember.identifier)}`
}

/** The invitee's `seed.accounts` entry, indented for the list it joins. */
function seedInviteeAccount(): string {
  return `    - identifier: ${quoted(reviewInvitee.identifier)}
      password: ${quoted(reviewInvitee.password)}
      display-name: ${quoted(reviewInvitee.displayName)}`
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
      `the review configuration template holds no ${label} needle ` +
        `(${JSON.stringify(needle)}); the template and the harness have drifted`,
    )
  }
  if (body.indexOf(needle, first + needle.length) !== -1) {
    throw new Error(
      `the review configuration template holds more than one ${label} needle ` +
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
      `the review configuration template holds ${found} ${label} needle(s) ` +
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
  const opsAddress = `127.0.0.1:${input.opsPort}`
  const origin = `http://localhost:${input.backendPort}`
  const opsOrigin = `http://localhost:${input.opsPort}`
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
    'address: "127.0.0.1:8083"',
    `address: ${quoted(opsAddress)}`,
    'operations listener',
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
  // server list. All three must follow the port the browser actually opens.
  body = replaceAll(
    body,
    '"http://localhost:8080"',
    quoted(origin),
    3,
    'public origin',
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
  if (extraAccounts.length > 0) {
    body = replaceOnce(
      body,
      seedOperatorAccount,
      `${seedOperatorAccount}\n${extraAccounts.join('\n')}`,
      'seed operator account',
    )
  }
  // The member the member-workspace journey signs in as also gets its
  // membership: the row that binds it to the tenant joins the seed's members.
  // The membership goes before `settings` so the key order the template
  // documents is kept. The invitee deliberately gets no membership: the
  // acceptance is what makes it a member.
  if (input.seedMember === true) {
    // The review template may already carry a members list (the impersonation
    // fixture seeds its target there). YAML rejects a mapping with the same key
    // twice, so the entry joins that existing list rather than opening a second
    // key: it is inserted at the end of the list, which is the line before the
    // next two-space key after it.
    const settingsNeedle = '\n  settings:\n'
    const membersNeedle = '\n  members:\n'
    const membersAt = body.indexOf(membersNeedle)
    if (membersAt === -1) {
      body = replaceOnce(
        body,
        settingsNeedle,
        `\n  members:\n${seedMemberMembership()}\n  settings:\n`,
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
      body = `${body.slice(0, insertAt)}${seedMemberMembership()}\n${body.slice(insertAt)}`
    }
  }

  // The impersonation fixture's second factors change how the seeded operator
  // signs in: every later sign-in must complete one. Only the journey that
  // needs it turns it on; every other server starts from the template's own
  // seed with the factors off.
  if (input.seedImpersonation !== true) {
    body = replaceOnce(
      body,
      '\n  impersonation: true\n',
      '\n  impersonation: false\n',
      'seed impersonation switch',
    )
  }

  await writeFile(path, body, { encoding: 'utf8', mode: 0o600 })

  return {
    path,
    dir: input.dir,
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
