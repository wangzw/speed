/**
 * J13 -- a deployer stands the deployment up on a fresh volume and hands it to
 * the operator the seed wrote.
 *
 * This is a fixture-type journey, not a pure browser one, by the requirements
 * author's ruling. The local tier cannot run the container entrypoint or reach
 * the Fly proxy, a real SMTP relay or a real OAuth application, so the parts
 * that are deployment machinery are driven as commands and the parts that are
 * product behaviour are driven in the browser:
 *
 *  - Command side (fixtures/deploy-server.ts): the deploy-tagged binary is
 *    built by the global setup; the fixture renders the deployment template
 *    (deploy/fly.yaml.tmpl) onto a temporary volume, runs the binary's own
 *    `sqlite seed`, then starts `sqlite serve` from that volume. The operations
 *    probes and the merged API document are read by HTTP from the operations
 *    listener, the login discovery document is read from the backend origin,
 *    and the first-run signing key's permission is read from the volume. This
 *    is the allowance the e2e README records for fixture-type journeys; no
 *    browser behaviour is replaced by it.
 *  - Browser side: one sign-in, driven with clicks and keys only. An anonymous
 *    viewer opens the protected account path and meets the sign-in surface; the
 *    seeded operator signs in with the username and password the seed wrote and
 *    the same path resumes with its content. The header console entry and the
 *    home console card appear; the account page shows the profile, the sign-in
 *    identifier and the standing, non-dismissible email notice; the console
 *    shows its grouped navigation and the roster-derived overview; and a
 *    management write from the still-unproved operator is refused with the
 *    guidance to verify the address on the account page.
 *
 * Not covered here, and why:
 *
 *  - The container entrypoint itself, including its secret injection and its
 *    requirement checks (REQ-deployment-015, 024, 025, 027), is not executed.
 *    The fixture renders the same template and runs seed then serve, but the
 *    entrypoint's own missing-secret refusals, value validation and write of
 *    the rendered document are shell behaviour the tier does not reproduce.
 *    REQ-deployment-026's missing/short encryption-key refusals belong to the
 *    same startup path and are likewise not driven.
 *  - A real SMTP relay (REQ-deployment-028, 029): no message is sent, so the
 *    success and failure records of an external call and the redaction of their
 *    fields are not observed. The rendered configuration points at a loopback
 *    relay that is never reached.
 *  - The Fly proxy and the forwarded client-address behaviour are outside the
 *    local harness.
 *  - Seed's ordering, idempotency and operator-overwrite protections
 *    (REQ-deployment-009, 010, 011) need a second seed run and a settings write
 *    through the console. This operator is unverified, so its write is refused
 *    before any value changes; the restart half of the journey is not driven.
 *  - Seed's alternate failure paths (REQ-deployment-013, 014) are not on this
 *    journey's main line.
 *  - REQ-deployment-001's single-executable claim is exercised for the HTTP and
 *    one-shot-command halves (the same binary seeds and serves page and API),
 *    but not for the in-process job delivery its acceptance also names, so it
 *    is not claimed.
 *  - REQ-deployment-017's down-state clause (the probe still answers 200 while
 *    the database or a backend is down) is not driven: stopping a declared
 *    endpoint would reach around the product. The claim is the probe's positive
 *    half -- an unauthenticated GET on the separate operations listener that
 *    answers 200 without a credential.
 *  - REQ-signin-004 (the login surface's set is derived from the discovery
 *    document) cannot be discriminated here: a hardcoded surface that listed
 *    the same four methods would pass the same assertions, so it is not
 *    claimed.
 *
 * The claims that are narrower than the requirement text:
 *
 *  - REQ-deployment-002 is claimed for the same-origin half: page and API
 *    answer on the one backend origin. That the deployment publishes no second
 *    service port is a topology fact this local run cannot observe.
 *  - REQ-deployment-005 is asserted by the fixture's start: the run does not
 *    answer `use` until the ready record has been written after every module
 *    started.
 *  - REQ-deployment-006 is claimed for the first-run generation and the
 *    owner-only permission; the token-survives-restart half is not driven.
 *  - REQ-deployment-007 is claimed for a plaintext-password account whose seed
 *    entry names an email address and a username: the seed writes both, and the
 *    password login accepts the username while the address remains an unproved
 *    identifier. The hash-only credential form is not driven.
 *  - REQ-deployment-008 is claimed for the one active tenant the seed registers:
 *    the overview's roster-derived total and active counts report it.
 *  - REQ-platform-admin-001 is claimed for the operator's side: the console
 *    entry and the home console card appear and name the same target. The
 *    ordinary-member and impersonating-session cases are not driven.
 *  - REQ-platform-admin-006 is claimed narrowly: the identity group renders as
 *    a disclosure control and the tenants page is reachable from it. The
 *    platform-configuration group folds to its one served entry in this
 *    assembly (no SMS-template page is served), so its group heading is not
 *    observable here.
 *  - REQ-platform-admin-007 is claimed for the total/active/disabled counts;
 *    the empty-roster case is not driven.
 *  - REQ-deployment-018 is claimed for its positive half only: with every
 *    declared endpoint and the database answering, `/ready` answers 200 and its
 *    body says ready. The 503 case that names each failed part is not driven.
 *  - REQ-signin-001 is claimed for the discovery document's assembled entry
 *    set: the document lists exactly the four methods this assembly serves,
 *    each with its rendering kind. The appear/disappear-when-toggled half is
 *    not driven, because it needs a second assembly or a runtime switch this
 *    tier does not perform.
 *  - REQ-platform-admin-017 and REQ-signup-027 are claimed for the refusal of a
 *    human write from an unproved operator and the guidance it renders; the
 *    step-up retry that must still be refused is not driven.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The command-side assertions read the product's own reports
 * (seed's output, the operations documents, the file permission), which are the
 * product's answers rather than interface copy.
 */
// @journey journey-deploy-and-operate.md
// @covers REQ-deployment-002, REQ-deployment-003, REQ-deployment-004, REQ-deployment-005, REQ-deployment-006, REQ-deployment-007, REQ-deployment-008, REQ-deployment-012, REQ-deployment-017, REQ-deployment-018, REQ-deployment-019, REQ-signin-001, REQ-signin-006, REQ-signin-030, REQ-signup-027, REQ-platform-admin-001, REQ-platform-admin-006, REQ-platform-admin-007, REQ-platform-admin-017
import type { Locator, Page } from '@playwright/test'

import { deployOperator } from '../fixtures/config'
import { expect, test } from '../fixtures/deploy'
import { copy } from '../helpers/i18n'

// The settings table's type column renders only from its container's `lg`
// width, so the desktop viewport here is wide enough for the item the refusal
// step names.
test.use({ viewport: { width: 1920, height: 1080 } })

/** The operator the deployment's seed writes. */
const OPERATOR = deployOperator

/** The entry document's one mount point: the bundle the binary embeds. */
const ENTRY_DOCUMENT_MARKER = '<div id="root"></div>'

/** The one declared setting the refused write targets, and its declared type. */
const SETTING_KEY = 'ratelimit.limit'
const SETTING_TYPE = 'int'

/** A value inside the item's declared domain; the write is refused unwritten. */
const NEW_VALUE = '150'

/** The operation the merged document must carry from a module no command runs. */
const UNRUN_MODULE_PATH = '/api/v1/system/sms/templates'
const UNRUN_MODULE_NAME = 'sms.admin'

/** One login method as the discovery document reports it. */
interface DiscoveryMethod {
  readonly name: string
  readonly kind: string
}

/**
 * The login methods the deploy assembly serves, as the discovery document
 * reports them: the two social methods (redirect buttons), the passkey
 * ceremony, and password (a form). These are the document's own stable method
 * names, not interface copy, so they are literals.
 */
const DISCOVERY_METHODS = [
  { name: 'github', kind: 'redirect' },
  { name: 'google', kind: 'redirect' },
  { name: 'passkey', kind: 'webauthn' },
  { name: 'password', kind: 'form' },
] as const

/** The account page's profile section heading. */
function profileHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('accountUi', 'profile.title'),
    exact: true,
  })
}

/** The account page's email-verification section. */
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

/** The account page's sign-in-identifiers section. */
function identifiersSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'identifiers.title'),
    exact: true,
  })
}

/** The email-verification notice under the app bar. */
function verificationBanner(page: Page, email: string): Locator {
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email },
    )}`,
    exact: true,
  })
}

/** The sign-in surface's page heading. */
function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The header entry that opens the console. */
function consoleEntry(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('link', { name: copy('demo', 'nav.console'), exact: true })
}

/** The home page's console card, as the link that carries it. */
function consoleCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.console.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

/** The console's navigation landmark (the platform navigation). */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The settings page's namespace navigation. */
function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'settings.nav.label'),
    exact: true,
  })
}

/** The settings table. */
function settingsTable(page: Page): Locator {
  return page.getByRole('table')
}

/** The table row of the setting the refused write targets. */
function settingRow(page: Page): Locator {
  return settingsTable(page)
    .getByRole('row')
    .filter({
      has: page.getByRole('textbox', { name: SETTING_KEY, exact: true }),
    })
}

/** The item's value field: the value cell is the draft input. */
function valueField(page: Page): Locator {
  return page.getByRole('textbox', { name: SETTING_KEY, exact: true })
}

/**
 * One overview KPI's value. The label is one paragraph and the count the next
 * sibling, and both sit inside the console's main landmark; scoping there keeps
 * the navigation's own "Tenants" group out of the answer.
 */
function kpiValue(page: Page, label: string): Locator {
  return page
    .getByRole('main')
    .getByText(label, { exact: true })
    .locator('xpath=following-sibling::p[1]')
}

/** Parse one JSON answer, failing with the body when it is not JSON. */
function jsonBody(answer: { readonly body: string }): Record<string, unknown> {
  try {
    return JSON.parse(answer.body) as Record<string, unknown>
  } catch (error) {
    throw new Error(
      `the answer was not JSON (${String(error)}); body:\n${answer.body}`,
    )
  }
}

test('deploy-and-operate: a deployment serves, seeds, and refuses an unproved operator write', async ({
  deploy,
}) => {
  const { page, server } = deploy

  await test.step('steps 3-4: the deploy assembly seeds a fresh volume and mints the signing key', async () => {
    // The fixture has already run the binary's own seed on a volume that never
    // saw a migration and started serve without a separate migrate step. The
    // seed's report is the deployment's own answer about what it wrote; the
    // same writes are exercised again by the sign-in and the roster count below.
    expect(server.seedOutput).toContain(
      `seeded: account ${OPERATOR.identifier}`,
    )
    expect(server.seedOutput).toContain('seeded: tenant system')
    // The template seeds no business tenant: the operator administers the
    // platform, and the only other tenant is the personal space the hook gives
    // the seeded account.
    expect(server.seedOutput).not.toContain('seeded: tenant acme')
    expect(server.seedOutput).toContain('seeded: role system/platform-admin')

    // The first run mints the token signing key on the volume with owner-only
    // permission; the key's bytes are never read by the tier.
    expect(server.signingKeyMode).toBe(0o600)
  })

  await test.step('step 5: one origin serves the entry document, its page fallback and 404 for named assets', async () => {
    const root = await server.probe('/')
    expect(root.status).toBe(200)
    expect(root.body).toContain(ENTRY_DOCUMENT_MARKER)

    // An unknown extension-less page path answers the same entry document: the
    // client-side router owns it.
    const unknownPage = await server.probe('/deploy/unknown/page')
    expect(unknownPage.status).toBe(200)
    expect(unknownPage.body).toBe(root.body)

    // A named static resource that the bundle does not carry is a 404, not the
    // entry document.
    const unknownAsset = await server.probe('/assets/not-a-built-asset.js')
    expect(unknownAsset.status).toBe(404)
    expect(unknownAsset.body).not.toBe(root.body)

    // The API answers on the same published origin as the page.
    const methods = await server.probe('/api/v1/auth/methods')
    expect(methods.status).toBe(200)
    expect(methods.contentType).toContain('application/json')

    // The discovery document lists exactly the methods this assembly serves
    // and not one it does not: password, the passkey ceremony and the two
    // social methods, each with the kind that says how the surface renders it.
    // The document is sorted by method name. (REQ-signin-001's runtime-toggle
    // half is not driven; see the file header.)
    const discovery = jsonBody(methods)
    const entries = (discovery.methods as readonly DiscoveryMethod[]).map(
      ({ name, kind }) => ({ name, kind }),
    )
    expect(entries).toEqual(DISCOVERY_METHODS)
  })

  await test.step('step 6: the protected account path meets an anonymous viewer with the sign-in surface', async () => {
    await page.goto(`${server.url}/account`)
    await expect(signInHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/account')

    // The methods the discovery document lists reach the surface: the password
    // form and the two redirect methods this deploy assembly holds.
    await expect(
      page.getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.identifierLabel'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.passwordLabel'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'passwordSignIn.submit'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'social.provider.google'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('button', {
        name: copy('authUi', 'social.provider.github'),
        exact: true,
      }),
    ).toBeVisible()

    // The sign-in surface is not the console: the console's navigation is
    // absent while the viewer is anonymous.
    await expect(consoleNav(page)).toHaveCount(0)
  })

  await test.step('step 7: the seeded operator signs in, the path resumes, and the console entries appear', async () => {
    const identifier = page.getByRole('textbox', {
      name: copy('authUi', 'passwordSignIn.identifierLabel'),
      exact: true,
    })
    await identifier.fill(OPERATOR.username)
    await page
      .getByRole('textbox', {
        name: copy('authUi', 'passwordSignIn.passwordLabel'),
        exact: true,
      })
      .fill(OPERATOR.password)
    await page
      .getByRole('button', {
        name: copy('authUi', 'passwordSignIn.submit'),
        exact: true,
      })
      .click()
    // The sign-in form leaves the document when the session flips. A refused
    // sign-in leaves it in place, and this wait then reports the failure.
    await identifier.waitFor({ state: 'detached' })

    // The requested path is resumed and renders its content rather than the
    // sign-in surface.
    expect(new URL(page.url()).pathname).toBe('/account')
    await expect(signInHeading(page)).toHaveCount(0)
    await expect(profileHeading(page)).toBeVisible()

    // The tenant home carries both console entries, and they name one target.
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    const entry = consoleEntry(page)
    const card = consoleCard(page)
    await expect(entry).toBeVisible()
    await expect(card).toBeVisible()
    const entryHref = await entry.getAttribute('href')
    expect(entryHref).not.toBeNull()
    await expect(card).toHaveAttribute('href', entryHref ?? '')
  })

  await test.step('step 8: the account page shows the profile and identifiers, and an unproved write is refused', async () => {
    // Opening the account page directly (not through the notice's verify
    // entry) is what the journey describes: the verification section waits for
    // a manual send, so no code has been sent on this arrival.
    await page.goto(`${server.url}/account`)
    await expect(profileHeading(page)).toBeVisible()
    await expect(
      page.getByLabel(copy('accountUi', 'profile.displayName.label'), {
        exact: true,
      }),
    ).toHaveValue(OPERATOR.displayName)

    const identifiers = identifiersSection(page)
    await expect(identifiers).toBeVisible()
    const held = identifiers
      .getByRole('listitem')
      .filter({ hasText: OPERATOR.identifier })
    await expect(held).toHaveCount(1)
    await expect(
      held.getByText(copy('accountUi', 'identifiers.unverified'), {
        exact: true,
      }),
    ).toBeVisible()

    // The standing notice names the address, offers both entries and carries no
    // close control.
    const banner = verificationBanner(page, OPERATOR.identifier)
    await expect(banner).toBeVisible()
    await expect(
      banner.getByRole('link', {
        name: copy('demo', 'verificationBanner.verify'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      banner.getByRole('link', {
        name: copy('demo', 'verificationBanner.change'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(banner.getByRole('button')).toHaveCount(0)

    // The section waits for the operator: the send control is offered and no
    // send has happened on arrival.
    const section = verificationSection(page)
    await expect(section).toBeVisible()
    await expect(
      section.getByRole('button', {
        name: copy('accountUi', 'verification.send'),
        exact: true,
      }),
    ).toBeEnabled()
    await expect(
      section
        .getByRole('status')
        .filter({
          hasText: copy('accountUi', 'verification.sent', {
            email: OPERATOR.identifier,
          }),
        }),
    ).toHaveCount(0)

    // Enter the console and attempt a settings write.
    await consoleEntry(page).click()
    await expect(consoleNav(page)).toBeVisible()
    await consoleNav(page)
      .getByRole('link', {
        name: copy('consoleUi', 'settings.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'settings.title'),
        exact: true,
      }),
    ).toBeVisible()
    await namespaceNav(page)
      .getByRole('button', { name: 'ratelimit', exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()

    const row = settingRow(page)
    await expect(row.getByText(SETTING_TYPE, { exact: true })).toBeVisible()
    await expect(
      row.getByText(copy('consoleUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()

    await valueField(page).fill(NEW_VALUE)
    await page
      .getByRole('button', {
        name: copy('consoleUi', 'settings.saveBar.save'),
        exact: true,
      })
      .click()

    // The refusal renders the code's own text -- the guidance to verify the
    // address on the account page -- and the draft stays in the field. The
    // item did not move off the declaration.
    const refusal = copy('consoleUi', 'errors.authn.contact_required')
    await expect(page.getByRole('alert').filter({ hasText: refusal }))
      .toBeVisible()
    await expect(valueField(page)).toHaveAccessibleDescription(refusal)
    await expect(valueField(page)).toHaveAttribute('aria-invalid', 'true')
    await expect(valueField(page)).toHaveValue(NEW_VALUE)
    await expect(
      row.getByText(copy('consoleUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('step 9: the console shows its grouped navigation and the roster-derived overview', async () => {
    // Back to the app, then in through the header entry the journey names.
    await page
      .getByRole('link', { name: copy('demo', 'console.backToApp'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await consoleEntry(page).click()

    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'dashboard.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The navigation is grouped: the multi-entry identity group renders as a
    // disclosure control, and the served pages are reachable from it.
    const nav = consoleNav(page)
    await expect(
      nav.getByRole('button', {
        name: copy('consoleUi', 'nav.groups.identity'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      nav.getByRole('link', {
        name: copy('consoleUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The overview's roster counts come from the deployment's tenant list. The
    // seed registers the system tenant, and the personal-tenant hook gives the
    // seeded operator its own space, so two active tenants stand in the roster
    // and nothing is disabled or deleted.
    await expect(
      kpiValue(page, copy('consoleUi', 'dashboard.kpi.tenants')),
    ).toHaveText('2')
    await expect(
      kpiValue(page, copy('consoleUi', 'dashboard.kpi.active')),
    ).toHaveText('2')
    await expect(
      kpiValue(page, copy('consoleUi', 'dashboard.kpi.disabled')),
    ).toHaveText('0')
    await expect(
      kpiValue(page, copy('consoleUi', 'dashboard.kpi.deleted')),
    ).toHaveText('0')
  })

  await test.step('step 10: the operations listener answers the liveness and readiness probes', async () => {
    // Liveness reads nothing and answers success on every request.
    const healthOne = await server.probeOps('/health')
    expect(healthOne.status).toBe(200)
    expect(jsonBody(healthOne)).toMatchObject({ status: 'ok' })
    const healthTwo = await server.probeOps('/health')
    expect(healthTwo.status).toBe(200)
    expect(jsonBody(healthTwo)).toMatchObject({ status: 'ok' })

    // Readiness answers once every declared endpoint and the database answer.
    const ready = await server.probeOps('/ready')
    expect(ready.status).toBe(200)
    expect(jsonBody(ready)).toMatchObject({ status: 'ready' })
  })

  await test.step('step 11: the merged document covers an operation no command runs', async () => {
    const answer = await server.probeOps('/openapi.json')
    expect(answer.status).toBe(200)
    const document = jsonBody(answer)
    const paths = document.paths as Record<
      string,
      Record<string, { tags?: string[] }>
    >
    const operation = paths[UNRUN_MODULE_PATH]?.get
    expect(operation).toBeDefined()
    // The operation is tagged with its declaring module, which the deploy
    // assembly imports for its contract but names in no command's run set.
    expect(operation?.tags ?? []).toContain(UNRUN_MODULE_NAME)
  })
})
