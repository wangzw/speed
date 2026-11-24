import type { BrowserContext, CDPSession, Page } from '@playwright/test'

export interface VirtualAuthenticator {
  // With presence off a ceremony stays pending, so a spec can observe the browser's waiting state.
  setPresent(present: boolean): Promise<void>
  // Turning user verification off makes a ceremony that requires it fail with NotAllowedError; this device cannot return an assertion whose user-verified flag is clear.
  setUserVerified(verified: boolean): Promise<void>
  close(): Promise<void>
}

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
    async setUserVerified(verified: boolean): Promise<void> {
      await session.send('WebAuthn.setUserVerified', {
        authenticatorId,
        isUserVerified: verified,
      })
    },
    async close(): Promise<void> {
      // Remove the authenticator before detaching so a context that outlives this helper stops answering ceremonies.
      await session
        .send('WebAuthn.removeVirtualAuthenticator', { authenticatorId })
        .catch(() => undefined)
      await session.detach().catch(() => undefined)
    },
  }
}
