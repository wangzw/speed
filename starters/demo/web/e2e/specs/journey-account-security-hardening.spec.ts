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
 * WHAT IS NOT ASSERTED. The sessions section renders the device summary, the
 * sign-in time, the last-active time (when it differs), the expiry and the
 * current-session marker. The last-active line appears only once a coalesced
 * touch moves it past the creation instant, which this run does not produce, so
 * it is not asserted. The other fields are, including the expiry, so
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
import { expect, test } from '../fixtures/demo'
import { copy, copyPattern } from '../helpers/i18n'
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

/** The home page's heading, the landing after a completed sign-in. */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The account page's heading. */
function accountHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.title'),
    exact: true,
  })
}

/** The header's account-settings entry. */
function accountLink(page: Page): Locator {
  return page.getByRole('link', {
    name: copy('demo', 'nav.account'),
    exact: true,
  })
}

/** One account section, named by its own title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The sign-in identifiers section. */
function identifiersSection(page: Page): Locator {
  return section(page, copy('accountUi', 'identifiers.title'))
}

/** The two-step-verification section. */
function mfaSection(page: Page): Locator {
  return section(page, copy('accountUi', 'mfa.title'))
}

/** The password section. */
function passwordSection(page: Page): Locator {
  return section(page, copy('accountUi', 'password.title'))
}

/** The sessions section. */
function sessionsSection(page: Page): Locator {
  return section(page, copy('accountUi', 'sessions.title'))
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
 * factor step offers the account's active factors; choosing the authenticator
 * app opens its six-digit challenge.
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
 * Click the enrolment wizard's confirmation when a settled answer parks the
 * flow's ready step as one further confirmation. The wizard closes itself when
 * the answer settles `enrolled`, so the call is a no-op then.
 */
async function confirmSettledEnrolment(mfa: Locator): Promise<void> {
  const confirm = mfa.getByRole('button', {
    name: copy('accountUi', 'mfa.enroll.confirmLabel'),
    exact: true,
  })
  if ((await confirm.count()) > 0 && (await confirm.isVisible())) {
    await confirm.click()
  }
}

/** The shared secret the rendered authenticator setup URI carries. */
function secretOf(setupUri: string): string {
  const secret = new URL(setupUri).searchParams.get('secret')
  if (secret === null || secret === '') {
    throw new Error(
      'the authenticator setup URI the page rendered carries no secret',
    )
  }
  return secret
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
      await expect(accountHeading(page)).toBeVisible()
      // The verification section reports the proved address, and the
      // identifiers section lists the same address as the account's mail.
      await expect(
        page.getByRole('region', {
          name: copy('accountUi', 'verification.verifiedTitle'),
          exact: true,
        }),
      ).toBeVisible()
      // The passkey ceremony is answered by Chromium's virtual authenticator.
      authenticator = await addVirtualAuthenticator(page.context(), page)
    })

    await test.step('step 1: the account page lists the identifiers with the email proved, and the empty two-step surface offers the enrolment entries', async () => {
      const identifiers = identifiersSection(page)
      await expect(identifiers.getByRole('listitem')).toHaveCount(2)

      const emailRow = identifiers
        .getByRole('listitem')
        .filter({ has: page.getByText(account.email, { exact: true }) })
      await expect(emailRow).toHaveCount(1)
      await expect(
        emailRow.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toBeVisible()

      // A username carries no ownership marker. The address contains the
      // username, so the row is found by the username's own exact text.
      const usernameRow = identifiers
        .getByRole('listitem')
        .filter({ has: page.getByText(account.username, { exact: true }) })
      await expect(usernameRow).toHaveCount(1)
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.verified'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        usernameRow.getByText(copy('accountUi', 'identifiers.unverified'), {
          exact: true,
        }),
      ).toHaveCount(0)

      // No method is set up yet, and the deployment's own entries are offered.
      const mfa = mfaSection(page)
      await expect(
        mfa.getByText(copy('accountUi', 'mfa.empty'), { exact: true }),
      ).toBeVisible()
      await expect(mfa.getByRole('listitem')).toHaveCount(0)
      await expect(
        mfa.getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.totp'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        mfa.getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.webauthn'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 2: the authenticator app is enrolled with a current code and becomes active', async () => {
      const mfa = mfaSection(page)
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.totp'),
          exact: true,
        })
        .click()

      // The wizard shows the setup URI as a read-only field beside the code
      // field: the URI is the value an authenticator app would import.
      const uriField = mfa.getByRole('textbox', {
        name: copy('accountUi', 'flowStep.enrollTotpUriLabel'),
        exact: true,
      })
      await expect(uriField).toBeVisible()
      secret = secretOf(await uriField.inputValue())

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
      // The accepted code leaves the code form; a settled flow then parks its
      // ready step as one further confirmation of the same control.
      await expect(codeField).toHaveCount(0, { timeout: 15_000 })
      await confirmSettledEnrolment(mfa)

      // The settled enrolment confirms itself and the list re-reads: one
      // active authenticator app.
      await expect(
        mfa.getByText(
          copy('accountUi', 'mfa.enroll.enrolled', {
            factor: copy('accountUi', 'mfa.factor.kind.totp'),
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(mfa.getByRole('listitem')).toHaveCount(1)
      await expect(
        mfa
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), {
            exact: true,
          })
          .first(),
      ).toBeVisible()
      await expect(
        mfa.getByText(copy('accountUi', 'mfa.factor.status.active'), {
          exact: true,
        }),
      ).toBeVisible()
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

    await test.step('step 5: a passkey is added after the waiting state, its cancel and its retry', async () => {
      await accountLink(page).click()
      await expect(accountHeading(page)).toBeVisible()
      const mfa = mfaSection(page)

      // Presence simulation is off for the first attempt: the ceremony stays
      // pending, which is exactly the waiting surface the journey describes.
      await authenticator?.setPresent(false)
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.webauthn'),
          exact: true,
        })
        .click()
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
      // The retried ceremony settles, and the accepted credential may leave a
      // parked ready step with the same confirmation control.
      await expect(
        mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
          exact: true,
        }),
      ).toHaveCount(0, { timeout: 20_000 })
      await confirmSettledEnrolment(mfa)

      await expect(
        mfa.getByText(
          copy('accountUi', 'mfa.enroll.enrolled', {
            factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
          }),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 20_000 })
      await expect(mfa.getByRole('listitem')).toHaveCount(2)
      await expect(
        mfa
          .getByText(copy('accountUi', 'mfa.factor.kind.webauthn'), {
            exact: true,
          })
          .first(),
      ).toBeVisible()
      await expect(
        mfa
          .getByText(copy('accountUi', 'mfa.factor.status.active'), {
            exact: true,
          })
          .first(),
      ).toBeVisible()
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
      await accountLink(page).click()
      await expect(accountHeading(page)).toBeVisible()
      const sessions = sessionsSection(page)
      await expect(sessions.getByRole('listitem')).toHaveCount(2)

      // Each row names its device and the sign-in time, and exactly one row is
      // the current session. The revoke control names the device it ends, so
      // its presence on the other row is the device summary's rendered proof.
      await expect(
        sessions
          .getByText(copyPattern('accountUi', 'sessions.signedIn', { time: '.+' }))
          .first(),
      ).toBeVisible()
      // Both live rows name their session's absolute expiry.
      await expect(
        sessions.getByText(
          copyPattern('accountUi', 'sessions.expires', { time: '.+' }),
        ),
      ).toHaveCount(2)
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
      // the current session remains.
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
        const link = accountLink(deviceB)
        if ((await link.count()) > 0) {
          await link.click({ timeout: 1_000 }).catch(() => undefined)
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
      // this session already proved and is therefore not offered.
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

      // The passkey step's own control starts the ceremony. Once it settles,
      // the step is replaced by the parked `complete` step, whose confirmation
      // carries the same label; waiting for the prompt to leave keeps the
      // second click off the start control while the ceremony is working.
      const verify = dialog.getByRole('button', {
        name: copy('accountUi', 'mfa.stepUp.confirmLabel'),
        exact: true,
      })
      await expect(verify).toBeVisible()
      await verify.click()
      await expect(
        dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
          exact: true,
        }),
      ).toHaveCount(0, { timeout: 30_000 })
      const parked = dialog.getByRole('button', {
        name: copy('accountUi', 'mfa.stepUp.confirmLabel'),
        exact: true,
      })
      await expect(parked).toHaveCount(1)
      await parked.click()
      await expect(dialog).toHaveCount(0, { timeout: 30_000 })

      // The won challenge replays the refused change exactly once; the write
      // rotates the session token and the page stays signed in. Staying on the
      // account surface depends on the product's identity-transition guard
      // discarding an anonymous read that lands under the rotated credential:
      // a hole there flashes the shell to the session-ended surface, and these
      // assertions are where it shows.
      await expect(
        page.getByText(copy('accountUi', 'password.changed'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(accountHeading(page)).toBeVisible()
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
