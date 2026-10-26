/**
 * SignInScreen's register mode under a host page heading, in a real
 * browser: the host names the page and the screen heads its own register
 * block, so the two headings must not carry the same words. The host's
 * page heading here uses the shipped register-entry label -- the demo's
 * sign-in page heads its register mode with exactly those words -- and
 * the case requires the page to hold one heading carrying them.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { makeHarness, methodList } from '@speed/test-utils/session-harness'
import { renderWithProviders } from '../test-utils/render.js'
import { SignInScreen } from './SignInScreen.js'
import enUS from './locales/en-US.json' with { type: 'json' }

/** The words the host's page heading carries. Read from the bundle, never
 * inlined: the host shows the same label on its register entry. */
const HOST_PAGE_TITLE = enUS.mode.registerEntry

describe('SignInScreen register heading in Chromium', () => {
  it('keeps one heading carrying the host page title at 375', async () => {
    await page.viewport(375, 900)
    const harness = makeHarness({})
    renderWithProviders(
      <>
        <h1>{HOST_PAGE_TITLE}</h1>
        <SignInScreen
          session={harness.session}
          discovery={methodList(
            [{ name: 'sms', kind: 'form', opens_account: true }],
            { signup: true },
          )}
          mode="register"
        />
      </>,
      { language: 'en-US' },
    )

    expect(
      screen.getAllByRole('heading', { name: HOST_PAGE_TITLE }),
    ).toHaveLength(1)
  })
})
