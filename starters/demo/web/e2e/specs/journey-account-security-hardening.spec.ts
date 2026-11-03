/**
 * The account-security-hardening journey -- an ordinary account enrols an
 * authenticator and a passkey, signs in with the second factor, signs a second
 * device in, revokes that device's session, and changes the password through a
 * step-up.
 *
 * THE PASSKEY IS A BROWSER-DEVICE FIXTURE. The journey's second factor of a
 * kind different from the login's channel is a passkey. Chromium's virtual
 * authenticator answers the product's own WebAuthn ceremony (the page still
 * calls `navigator.credentials.create`/`.get`); the harness attaches it over
 * the one CDP family the honesty gate allows and switches presence simulation
 * so the waiting state and its cancel/retry controls are observable. Nothing
 * else is reached around the browser.
 *
 * THE FRESHNESS WINDOW IS WAITED OUT, NOT MANUFACTURED. A credential write by
 * an account that holds an active factor asks for a fresh local multi-factor
 * authentication, whose window is the setting `authn.mfa.recent-window`
 * (REQ-security-013). This server lowers that setting to its declared domain
 * floor of one minute, so the documented step-up is reachable inside a browser
 * run; the spec still waits for the real window to pass before submitting the
 * password change. The wait spans a backend window and is the only timed wait
 * in the spec.
 *
 * TOTP CODES ARE SINGLE USE PER WINDOW. The account uses an authenticator code
 * at enrollment and at every later second factor. REQ-security-012 refuses a
 * code accepted earlier in the same thirty-second window, so the spec tracks
 * the window it last used and waits for the next one before presenting another
 * code; it never re-submits a code.
 *
 * A REGISTRATION THE READER GIVES UP IS RELEASED AT ONCE. Starting an
 * enrollment writes a pending factor row on the server. The journey enters an
 * authenticator-app enrollment far enough to have that row written, cancels the
 * wizard, and rereads the list: the pending row is gone and the active factor
 * the account already held is untouched. The reread waits for the product's own
 * flow-release request, so it observes what the server kept rather than a list
 * the section has not re-read.
 *
 * WHAT IS NOT ASSERTED. The sessions section renders one table row per live
 * session, with the device summary, the address, the last-active time and the
 * row's action column, and marks the current session. The table's header names
 * all four columns and exactly one row carries the current marker, so
 * REQ-security-022 is claimed. The step-up's alternate
 * branches -- cancelling the dialog, and an account with only one factor that
 * offers a re-login exit (REQ-security-017) -- are unreachable here because
 * the journey enrols two factor kinds and completes the dialog. Two
 * requirement clauses are also left undriven, so their ids stay out of
 * `@covers`: REQ-security-002's refused-code clause (only the accepted path is
 * driven), and REQ-security-015's "a method registered during this session is
 * not offered" clause -- the only kind this account can register after the
 * session's authentication is the authenticator app, the channel the session
 * already proved, so its exclusion cannot be told apart from the channel rule;
 * only the other clause, that a method enabled before the session on a
 * different channel is offered, is driven. One wrong-current-password check is
 * not driven either.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-account-security-hardening.md
// @covers REQ-account-007, REQ-security-001, REQ-security-003, REQ-security-008, REQ-security-013, REQ-security-014, REQ-security-022, REQ-security-023, REQ-security-025, REQ-security-030, REQ-signin-006
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { createAccounts, type Accounts } from '../helpers/accounts'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { expect, test } from '../fixtures/demo'
import { copy, copyPattern } from '../helpers/i18n'
import { fillFactorName } from '../helpers/mfa'
import { totpCode } from '../helpers/totp'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The account self-service freshness window this server lowers to the
// setting's domain floor, so the step-up branch is reachable in a browser run.
test.use({ recentWindow: '1m', viewport: { width: 1440, height: 1200 } })

// The file's per-test bound covers fixture assembly as well as the journey:
// the server's seed, start and probes run inside the same clock, so a slow
// assembly reports the harness's own diagnostics instead of a bare timeout.
test.describe.configure({ timeout: 300_000 })

/** The lowered `authn.mfa.recent-window`, in milliseconds. */
const RECENT_WINDOW_MS = 60_000

/** The margin added on top of the window before a write must be refused. */
const WINDOW_MARGIN_MS = 3_000

/** The password the account holds after the change. */
const NEW_PASSWORD = 'e2e-Password-456'

// The names this journey gives its factors. The account surface lists each
// factor row under the name it was enrolled with; the sign-in and step-up
// surfaces name every offered factor by its kind, so the selections there read
// the kind's language-pack label.
const PASSKEY_FACTOR_NAME = 'Backup passkey'

// The name given to an enrollment this journey opens only to cancel it. If the
// wizard's cancel left the flow open, the server's pending row would carry this
// name back onto the list.

/** The home page's heading, the landing after a completed sign-in. */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** One account pane's heading, the page's one h1. */
function paneHeading(
  page: Page,
  section: 'profile' | 'security' | 'sessions' | 'preferences',
): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', `account.sections.${section}`),
    exact: true,
  })
}

/** One account section card, named by its own h2 title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The sign-in ways card (the identifiers on their sign-in axis). */
function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
}

/** The two-step-verification card. */
function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

/** The factor kinds the review assembly serves, each its own group. */
type MfaKind = 'totp' | 'webauthn' | 'sms'

/**
 * One method group inside the two-step-verification card, named by its own
 * heading. While an enrollment is open the heading names the enrollment, so a
 * group is addressed by its kind only while it lists that kind's entries.
 */
function mfaGroup(page: Page, kind: MfaKind): Locator {
  return mfaSection(page).getByRole('region', {
    name: copy('accountUi', `mfa.factor.kind.${kind}`),
    exact: true,
  })
}

/**
 * The add action on a method group's heading, for a group a plain add still
 * belongs to: a kind held several times, or a group with nothing active yet.
 */
function addFactorAction(page: Page, kind: MfaKind): Locator {
  const method = copy('accountUi', `mfa.factor.kind.${kind}`)
  return mfaGroup(page, kind).getByRole('button', {
    name: copy('accountUi', 'mfa.addAria', { method }),
    exact: true,
  })
}

/** The authenticator group's action once one entry is active: enrolling
 * another replaces it, so the action says so. */
function replaceFactorAction(page: Page): Locator {
  return mfaGroup(page, 'totp').getByRole('button', {
    name: copy('accountUi', 'mfa.replaceAria'),
    exact: true,
  })
}

/** The password card. */
function passwordSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.password'))
}

/** The sessions card. */
function sessionsSection(page: Page): Locator {
  return section(page, copy('demo', 'account.blocks.devices'))
}

/** The step-up dialog. */
function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
}

/** The session-ended surface the shell converges on after a session ends. */
function sessionEndedHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('authUi', 'sessionEnded.title'),
    exact: true,
  })
}

/** The sign-in surface's password fields and submit control. */
function identifierField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.identifierLabel'),
    exact: true,
  })
}
function passwordField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.passwordLabel'),
    exact: true,
  })
}
function signInButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordSignIn.submit'),
    exact: true,
  })
}

/**
 * The TOTP windows this spec has presented a code in. A code accepted in one
 * window cannot be presented again in the same window (REQ-security-012), and
 * a thirty-second window is shorter than the time some of these steps take.
 */
let lastCodeStep = -1

// The tracker belongs to one test's run, not to the module: a second test in
// this file (or a repeat) must not inherit a window the first one used.
test.beforeEach(() => {
  lastCodeStep = -1
})

/**
 * A code for the next window this spec has not used, computing it at the
 * moment it is typed. The wait is not a render state: it is the authenticator's
 * own thirty-second window, which a second submission in the same window would
 * be refused for. The step is sampled once and the code generated for that
 * same step, so a window boundary falling between the two would not stamp a
 * code from one window as the last used in another.
 */
async function nextTotpCode(secret: string): Promise<string> {
  const stepMs = 30_000
  let step = Math.floor(Date.now() / stepMs)
  if (step <= lastCodeStep) {
    const resumeAt = (lastCodeStep + 1) * stepMs + 500
    const wait = resumeAt - Date.now()
    if (wait > 0) {
      await new Promise<void>((resolve) => setTimeout(resolve, wait))
    }
    step = Math.floor(Date.now() / stepMs)
  }
  lastCodeStep = step
  return totpCode(secret, new Date(step * stepMs))
}

/**
 * Complete a password sign-in's second factor with the authenticator app. The
 * account holds one authenticator app, so the factor step names that entry by
 * its kind; choosing it opens the six-digit challenge.
 */
async function completeTotpFactor(page: Page, secret: string): Promise<void> {
  // The step belongs to the password channel, which renders in its own panel.
  // The enterprise entry sits on the same sign-in page and carries its own
  // "Continue", so every control of the step is addressed within that panel.
  const panel = page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  const choice = panel.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.totp'),
    exact: true,
  })
  await expect(choice).toBeVisible()
  await choice.click()

  const codeField = panel.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
  await expect(codeField).toBeVisible()
  await codeField.fill(await nextTotpCode(secret))
  await panel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })
}

/**
 * Read the base32 setup key the wizard's manual-entry disclosure shows. The
 * panel groups the key into four-character blocks for hand entry; an
 * authenticator takes it without the spaces.
 */
async function revealedTotpSecret(mfa: Locator): Promise<string> {
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'flowStep.totpManualToggle'),
      exact: true,
    })
    .click()
  const secret = mfa.locator('code')
  await expect(secret).toHaveCount(1)
  await expect(secret).toBeVisible()
  return (await secret.innerText()).replace(/\s+/gu, '')
}

/** Wait until the given instant has passed, for a backend window. */
async function waitUntil(instant: number): Promise<void> {
  const wait = instant - Date.now()
  if (wait > 0) {
    await new Promise<void>((resolve) => setTimeout(resolve, wait))
  }
}

/** Turn the sign-in surface into the password form on a device with no session. */
async function openSignIn(page: Page, url: string): Promise<void> {
  await page.goto(`${url}/`)
  await expect(
    page.getByRole('heading', {
      name: copy('demo', 'signIn.heading'),
      exact: true,
    }),
  ).toBeVisible()
}

test('account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up', async ({
  demo,
  browser,
}) => {
  // Registration, two sign-ins on device A, a second device, a revoked device,
  // the freshness window and the step-up's own clicks. The file-level
  // `test.describe.configure` above sets the bound for this test, fixture
  // assembly included.

  const { page, server } = demo
  const accounts: Accounts = demo.accounts
  const account = accounts.unique('security')
  let secret = ''
  let deviceBContext: BrowserContext | undefined
  let oldPasswordContext: BrowserContext | undefined
  let deviceBPage: Page | undefined
  let authenticator: VirtualAuthenticator | undefined
  // When device A completed its second-factor sign-in at step 6; the password
  // change at step 10 must come after this session's freshness window.
  let step6AuthenticatedAt = 0

  try {
    await test.step('precondition: an ordinary account is registered and its email address is proved', async () => {
      await accounts.registerAndVerify(account)
      await expect(paneHeading(page, 'profile')).toBeVisible()
      // The email card reports the proof it completed, on the same profile
      // pane that owns the address.
      await expect(
        page.getByText(
          copy('accountUi', 'verification.verifiedDescription', {
            email: account.email,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      // The passkey ceremony is answered by Chromium's virtual authenticator.
      authenticator = await addVirtualAuthenticator(page.context(), page)
    })

    await test.step('step 1: the account page lists the identifiers with the email proved, and the empty two-step surface offers the enrolment entries', async () => {
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      const identifiers = identifiersSection(page)
      // The sign-in axis shows one group per kind of login name. The account
      // holds a username and one proved address, and the proved address is
      // the one chosen to sign in with; no phone number signs in.
      await expect(
        identifiers.getByRole('heading', {
          name: copy('accountUi', 'identifiers.kind.username'),
          exact: true,
        }),
      ).toBeVisible()
      // The username is the account's login name, named in its group's
      // sentence (the address contains the username, so the exact text is
      // what tells it apart).
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usage.loginAs', {
            kind: copy('accountUi', 'identifiers.kind.username'),
            value: account.username,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      // The username is fixed: its group states the rule and carries no row
      // action (a removable row would carry its own verify and remove
      // actions).
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usernamePermanent'),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        identifiers
          .getByRole('group', {
            name: copy('accountUi', 'identifiers.kind.username'),
            exact: true,
          })
          .getByRole('button'),
      ).toHaveCount(0)
      // The email group lists the proved addresses only, and the one on this
      // account is the selected choice.
      await expect(
        identifiers.getByRole('combobox', {
          name: copy('accountUi', 'identifiers.kind.email'),
          exact: true,
        }),
      ).toContainText(account.email)
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usage.phoneNone'),
          { exact: true },
        ),
      ).toBeVisible()

      // No method is set up yet, and the deployment's own groups are offered.
      // The section renders one group per method the deployment serves (the
      // review assembly serves the authenticator app, the passkey and the text
      // message), each named by its heading and carrying one add action on
      // that heading. No group lists a row before anything is enrolled, and
      // with no enabled row each action words itself as a plain add.
      const servedMethods = ['totp', 'webauthn', 'sms'] as const
      for (const kind of servedMethods) {
        const group = mfaGroup(page, kind)
        await expect(
          group.getByRole('heading', {
            level: 3,
            name: copy('accountUi', `mfa.factor.kind.${kind}`),
            exact: true,
          }),
        ).toBeVisible()
        const add = addFactorAction(page, kind)
        await expect(add).toBeVisible()
        await expect(add).toHaveText(copy('accountUi', 'mfa.action.add'))
        await expect(group.getByRole('listitem')).toHaveCount(0)
      }
    })

    await test.step('step 2: the authenticator app is enrolled with a current code and becomes active', async () => {
      const mfa = mfaSection(page)
      await addFactorAction(page, 'totp').click()
      // The authenticator entry carries no name: the panel shows the method's
      // body alone.
      await expect(
        mfa.getByRole('textbox', {
          name: copy('accountUi', 'mfa.enroll.labelField'),
          exact: true,
        }),
      ).toHaveCount(0)

      // The panel shows the setup URI as a scannable symbol; the manual
      // disclosure reveals the base32 key behind it for setup by hand.
      await expect(
        mfa.getByRole('img', {
          name: copy('accountUi', 'flowStep.totpQrLabel'),
          exact: true,
        }),
      ).toBeVisible()
      secret = await revealedTotpSecret(mfa)

      const codeField = mfa.getByRole('textbox', {
        name: copy('accountUi', 'flowStep.codeLabel'),
        exact: true,
      })
      await expect(codeField).toBeVisible()
      await codeField.fill(await nextTotpCode(secret))
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.confirmLabel'),
          exact: true,
        })
        .click()
      // One confirmation takes the whole flow: the ready step that follows the
      // code is submitted without another click, and the panel closes.
      await expect(codeField).toHaveCount(0, { timeout: 15_000 })

      // The settled enrolment confirms itself and the list re-reads: the
      // authenticator group lists the one enabled row under its own heading.
      await expect(
        mfa.getByText(
          copy('accountUi', 'mfa.enroll.enrolled', {
            factor: copy('accountUi', 'mfa.factor.kind.totp'),
          }),
          { exact: true },
        ),
      ).toBeVisible()
      const totpGroup = mfaGroup(page, 'totp')
      await expect(totpGroup.getByRole('listitem')).toHaveCount(1)
      // The single authenticator row reads as its kind; the group's heading
      // carries the same words, so the assertion is scoped to the row.
      await expect(
        totpGroup
          .getByRole('listitem')
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
      ).toBeVisible()
      // With an enabled authenticator entry, the group's action offers the
      // replacement instead of another add.
      await expect(replaceFactorAction(page)).toHaveText(
        copy('accountUi', 'mfa.action.replace'),
      )
    })

    await test.step('step 3: the account signs out of device A and returns to the sign-in surface', async () => {
      await accounts.signOut()
      await expect(sessionEndedHeading(page)).toBeVisible()
    })

    await test.step('step 4: a password sign-in stops at the second factor, and the authenticator app completes it', async () => {
      await accounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      // The account holds an active factor, so the first factor alone does not
      // establish the session: the factor step is on screen before the app.
      await expect(
        page.getByText(copy('authUi', 'signInFlow.factorTitle'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)
      await completeTotpFactor(page, secret)
    })

    await test.step('step 4b: cancelling an enrollment wizard leaves the factor list as it was', async () => {
      // The sign-in just completed is a fresh local multi-factor one, which is
      // what opening a registration for an account that already holds an
      // active factor asks for; the account page is reached from the home
      // surface that sign-in landed on.
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const mfa = mfaSection(page)
      // The rows before the wizard: the one enabled authenticator enrolled in
      // step 2. A pending row is the enrollment's own intermediate state and
      // is never listed, so none reads now.
      await expect(
        mfa
          .getByRole('listitem')
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
      ).toBeVisible()
      const rowsBefore = await mfa.getByRole('listitem').allInnerTexts()
      expect(rowsBefore).toHaveLength(1)
      expect(rowsBefore.join('\n')).toContain(
        copy('accountUi', 'mfa.factor.kind.totp'),
      )
      await expect(
        mfa.getByText(copy('accountUi', 'mfa.factor.status.pending'), {
          exact: true,
        }),
      ).toHaveCount(0)

      // The authenticator app is the entry that needs no device ceremony: its
      // panel asks for a code an app would show, so giving it up never needs
      // one. A group whose method already has an enabled row offers the
      // replacement action.
      await replaceFactorAction(page).click()
      // The replacement panel carries no name field.
      await expect(
        mfa.getByRole('textbox', {
          name: copy('accountUi', 'mfa.enroll.labelField'),
          exact: true,
        }),
      ).toHaveCount(0)
      // The flow starts with the panel and writes a pending factor row; the
      // panel is parked on the authenticator step, where the reader gives it up.
      await expect(
        mfa.getByRole('img', {
          name: copy('accountUi', 'flowStep.totpQrLabel'),
          exact: true,
        }),
      ).toBeVisible()

      // The wizard's cancel releases the flow on the server. The release is a
      // request the product sends itself, so the reread below waits for its
      // answer rather than racing the navigation that follows.
      const released = page.waitForResponse(
        (response) =>
          response.request().method() === 'DELETE' &&
          new URL(response.url()).pathname === '/api/v1/auth/flows/current',
        { timeout: 15_000 },
      )
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.cancelLabel'),
          exact: true,
        })
        .click()
      expect((await released).ok()).toBe(true)

      // Back on the entries, then a fresh read of the list: the cancelled
      // registration left no row behind, so the list holds the same rows with
      // the same names as before the wizard opened.
      await expect(replaceFactorAction(page)).toBeVisible()
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const reread = mfaSection(page)
      await expect(reread.getByRole('listitem')).toHaveCount(rowsBefore.length)
      expect(await reread.getByRole('listitem').allInnerTexts()).toEqual(
        rowsBefore,
      )
      await expect(
        reread
          .getByRole('listitem')
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
      ).toHaveCount(1)
      await expect(
        reread.getByText(copy('accountUi', 'mfa.factor.status.pending'), {
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('step 5: a passkey is added after the waiting state, its cancel and its retry', async () => {
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const mfa = mfaSection(page)

      // Presence simulation is off for the first attempt: the ceremony stays
      // pending, which is exactly the waiting surface the journey describes.
      await authenticator?.setPresent(false)
      await addFactorAction(page, 'webauthn').click()
      await fillFactorName(mfa, PASSKEY_FACTOR_NAME)
      await expect(
        mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
          exact: true,
        }),
      ).toBeVisible()
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.confirmLabel'),
          exact: true,
        })
        .click()
      await expect(
        mfa.getByText(copy('accountUi', 'flowStep.passkeyWaiting'), {
          exact: true,
        }),
      ).toBeVisible()
      // The wizard renders a Cancel of its own below the step, and the
      // ceremony's Cancel carries the same text. Scope to the step's own block
      // through the prompt it renders, so the choice never depends on DOM
      // order or on which sentence the two controls happen to share.
      const ceremony = mfa
        .getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
          exact: true,
        })
        .locator('xpath=..')
      const ceremonyCancel = ceremony.getByRole('button', {
        name: copy('accountUi', 'flowStep.passkeyCancel'),
        exact: true,
      })
      await expect(ceremonyCancel).toBeVisible()

      // Cancelling the ceremony fails the attempt and offers the retry; the
      // retry completes against the device.
      await ceremonyCancel.click()
      await expect(
        mfa.getByText(copy('accountUi', 'flowStep.passkeyCancelled'), {
          exact: true,
        }),
      ).toBeVisible()
      await authenticator?.setPresent(true)
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'flowStep.passkeyRetry'),
          exact: true,
        })
        .click()
      // The retried ceremony settles; the ready step that follows is
      // submitted without another click.
      await expect(
        mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
          exact: true,
        }),
      ).toHaveCount(0, { timeout: 20_000 })

      await expect(
        mfa.getByText(
          copy('accountUi', 'mfa.enroll.enrolled', {
            factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
          }),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 20_000 })
      // The passkey group lists the one enabled passkey now.
      const passkeyGroup = mfaGroup(page, 'webauthn')
      await expect(passkeyGroup.getByRole('listitem')).toHaveCount(1)
      await expect(
        passkeyGroup.getByText(PASSKEY_FACTOR_NAME, { exact: true }),
      ).toBeVisible()
      // With an enabled passkey, that group still offers a plain add.
      await expect(addFactorAction(page, 'webauthn')).toHaveText(
        copy('accountUi', 'mfa.action.add'),
      )
    })

    await test.step('step 6: device A signs in again with its password and the authenticator app', async () => {
      await accounts.signOut()
      await accounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      await completeTotpFactor(page, secret)
      step6AuthenticatedAt = Date.now()
    })

    await test.step('step 7: device B signs in with the same account and both devices stay signed in', async () => {
      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      deviceBContext = await browser.newContext(options)
      deviceBPage = await deviceBContext.newPage()
      const deviceB = createAccounts({ page: deviceBPage, server })
      await deviceB.signIn({
        identifier: account.username,
        password: account.password,
      })
      await completeTotpFactor(deviceBPage, secret)
      // Device A's session survives the second sign-in.
      await expect(homeHeading(page)).toBeVisible()
    })

    await test.step('step 8: the session list marks the current device and device B is revoked', async () => {
      await openAccountSection(page, 'sessions')
      const sessions = sessionsSection(page)
      // One list item per live session, no table and no header rail.
      await expect(sessions.getByRole('listitem')).toHaveCount(2)
      await expect(sessions.getByRole('table')).toHaveCount(0)
      await expect(sessions.getByRole('columnheader')).toHaveCount(0)
      // Each item's second line names its address and last activity.
      const activityPrefix = copy('accountUi', 'sessions.rowActivity', {
        time: '',
      })
      await expect(
        sessions.getByText(activityPrefix, { exact: false }),
      ).toHaveCount(2)

      // Each row names its device, its address and its last activity, and
      // exactly one row is the current session. The revoke control names the
      // device it ends, so its presence on the other row is the device
      // summary's rendered proof.
      const revoke = sessions.getByRole('button', {
        name: copyPattern('accountUi', 'sessions.revokeAriaWithDevice', {
          device: '.+',
        }),
      })
      await expect(revoke).toHaveCount(1)
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)
      await revoke.click()
      // The list converges on the server's answer: the revoked row is gone and
      // the current session remains, leaving the header and one row.
      await expect(sessions.getByRole('listitem')).toHaveCount(1)
      await expect(
        sessions.getByText(
          copyPattern('accountUi', 'sessions.revokedOne', { device: '.+' }),
        ),
      ).toBeVisible()
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)
    })

    await test.step("step 9: device B's next request lands on the session-ended surface", async () => {
      if (deviceBPage === undefined) {
        throw new Error('device B was never opened')
      }
      const deviceB = deviceBPage
      const ended = sessionEndedHeading(deviceB)
      // The revocation may also end the frame through the live notification
      // stream before this point. Until the ended surface appears, drive the
      // next request the step describes -- open the account page from the
      // header -- and retry the attempt, because a click may land while the
      // shell is swapping identities. The assertion is on the rendered ended
      // surface, not on the click.
      await expect(async () => {
        if (await ended.isVisible()) {
          return
        }
        const trigger = deviceB
          .getByTestId('account-entry-trigger')
        if ((await trigger.count()) > 0) {
          await trigger.click({ timeout: 1_000 }).catch(() => undefined)
          await deviceB
            .getByRole('menuitem', {
              name: copy('demo', 'account.sections.profile'),
              exact: true,
            })
            .click({ timeout: 1_000 })
            .catch(() => undefined)
        } else {
          // No header left to click: the next request is the reload.
          await deviceB.reload().catch(() => undefined)
        }
        if (!(await ended.isVisible())) {
          throw new Error(
            'the revoked device still shows an authenticated surface after a request',
          )
        }
      }).toPass({ timeout: 15_000, intervals: [500] })
      await expect(ended).toBeVisible()
      await expect(
        deviceB.getByRole('button', {
          name: copy('authUi', 'sessionEnded.signInAction'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(homeHeading(deviceB)).toHaveCount(0)
    })

    await test.step('step 10: the password change is refused for freshness and the step-up replays it after the passkey', async () => {
      // The session's multi-factor proof has to be older than the lowered
      // freshness window. This wait spans that backend window; it is not a
      // reaction to a render state.
      await waitUntil(
        step6AuthenticatedAt + RECENT_WINDOW_MS + WINDOW_MARGIN_MS,
      )

      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      const password = passwordSection(page)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        })
        .fill(account.password)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.confirm.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('button', {
          name: copy('accountUi', 'password.submit'),
          exact: true,
        })
        .click()

      // The refused write hands the pending change to the step-up dialog. The
      // dialog offers only the passkey: the authenticator app is the channel
      // this session already proved and is therefore not offered. With one
      // factor of each kind, the dialog names each entry by its kind.
      const dialog = stepUpDialog(page)
      await expect(dialog).toBeVisible({ timeout: 15_000 })
      await expect(
        dialog.getByText(copy('accountUi', 'mfa.stepUp.description'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        dialog.getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.webauthn'),
          exact: true,
        }),
      ).toHaveCount(1)
      await expect(
        dialog.getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.totp'),
          exact: true,
        }),
      ).toHaveCount(0)

      await dialog
        .getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.webauthn'),
          exact: true,
        })
        .click()

      // Choosing the passkey factor is the whole interaction: the assertion
      // step starts the browser ceremony as soon as it arrives, so there is
      // no start control to click. When the ceremony settles, the ready
      // state's `complete` step is the dialog's own confirmation -- it
      // answers that step without a click -- so the dialog closes on its
      // own. The prompt leaving is what tells that settled step apart from
      // the start control it replaces.
      await expect(
        dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
          exact: true,
        }),
      ).toHaveCount(0, { timeout: 30_000 })
      await expect(dialog).toHaveCount(0, { timeout: 30_000 })

      // The won challenge replays the refused change exactly once: the
      // landed write renders its success notice, while a second replay would
      // carry the now-stale current password and surface that refusal, which
      // no alert here may show. The write rotates the session token and the
      // page stays signed in. Staying on the account surface depends on the
      // product's identity-transition guard discarding an anonymous read that
      // lands under the rotated credential: a hole there flashes the shell to
      // the session-ended surface, and these assertions are where it shows.
      await expect(
        page.getByText(copy('accountUi', 'password.changed'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(
        page.getByText(copy('accountUi', 'password.currentRejected'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(paneHeading(page, 'security')).toBeVisible()
      await selectAccountSection(page, 'sessions')
      // The header row and the one remaining session.
      await expect(sessionsSection(page).getByRole('listitem')).toHaveCount(1)
    })

    await test.step('result: the new password signs in and the old one is refused', async () => {
      if (deviceBPage === undefined || deviceBContext === undefined) {
        throw new Error('device B was never opened')
      }

      // Device B was revoked, so it signs in afresh with the new password and
      // its second factor; that proves the password change took effect.
      const deviceB = createAccounts({ page: deviceBPage, server })
      await deviceB.signIn({
        identifier: account.username,
        password: NEW_PASSWORD,
      })
      await completeTotpFactor(deviceBPage, secret)

      // A device with no session is refused the old password.
      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      oldPasswordContext = await browser.newContext(options)
      const oldPasswordPage = await oldPasswordContext.newPage()
      await openSignIn(oldPasswordPage, server.url)
      await identifierField(oldPasswordPage).fill(account.username)
      await passwordField(oldPasswordPage).fill(account.password)
      await signInButton(oldPasswordPage).click()
      await expect(
        oldPasswordPage.getByText(copy('authUi', 'passwordSignIn.loginFailed'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
    })
  } finally {
    // The passkey device and the extra contexts are the spec's own; closing
    // them keeps a failed run from holding the notification stream open.
    await authenticator?.close().catch(() => undefined)
    await oldPasswordContext?.close().catch(() => undefined)
    await deviceBContext?.close().catch(() => undefined)
  }
})
