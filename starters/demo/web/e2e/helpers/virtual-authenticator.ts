/**
 * The WebAuthn virtual-authenticator device fixture.
 *
 * The passkey journey needs a device that can complete a real WebAuthn
 * ceremony. Chromium exposes one over CDP, and the requirements author rules
 * that fixture a browser device rather than a backend: the page still calls
 * `navigator.credentials.create`/`.get` through the product's own code, and
 * the virtual authenticator answers exactly as a platform authenticator
 * would. Every command this module sends is in the one CDP family the honesty
 * gate allows (`WebAuthn.*`), and each is a literal so the gate can prove it.
 *
 * Presence simulation is switchable because the journey also observes the
 * surface while the browser waits for a device: with presence off, a ceremony
 * stays pending, so the wizard's waiting state and its cancel/retry controls
 * are observable. Turning it on before the next attempt lets the retried
 * ceremony settle.
 *
 * The authenticator lives on the page target's CDP session. A page navigation
 * keeps the same target, so the credential registered during enrollment stays
 * available to the later step-up assertion.
 */
import type { BrowserContext, CDPSession, Page } from '@playwright/test'

export interface VirtualAuthenticator {
  /**
   * Whether a ceremony that asks for user presence completes at once. With
   * this off the request stays pending until it is cancelled or presence is
   * turned back on.
   */
  setPresent(present: boolean): Promise<void>
  /** Detach the CDP session that owns the authenticator. */
  close(): Promise<void>
}

/**
 * Attach one virtual authenticator to the page's target and answer with the
 * controls a spec needs. Presence starts on, which is the ordinary case; a
 * spec that wants to observe the waiting state turns it off first.
 */
export async function addVirtualAuthenticator(
  context: BrowserContext,
  page: Page,
): Promise<VirtualAuthenticator> {
  const session: CDPSession = await context.newCDPSession(page)
  await session.send('WebAuthn.enable', { enableUI: false })
  const { authenticatorId } = await session.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })
  return {
    async setPresent(present: boolean): Promise<void> {
      await session.send('WebAuthn.setAutomaticPresenceSimulation', {
        authenticatorId,
        enabled: present,
      })
    },
    async close(): Promise<void> {
      // Remove the authenticator before detaching, so cleanup does not rest on
      // the page context being destroyed: a context that outlives this helper
      // would otherwise keep answering ceremonies with it. A target that is
      // already gone is the one case the command fails in, and detaching is
      // then all that remains.
      await session
        .send('WebAuthn.removeVirtualAuthenticator', { authenticatorId })
        .catch(() => undefined)
      await session.detach().catch(() => undefined)
    },
  }
}
