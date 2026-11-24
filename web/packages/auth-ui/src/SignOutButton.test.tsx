import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { buttonClasses } from '@mui/material/Button'
import { switchLanguage } from '@speed/i18n'
import { createAppTheme } from '@speed/ui-kit'
import { SignOutButton } from './SignOutButton.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGOUT,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  makeHarness,
  principal,
  type Harness,
} from '@speed/test-utils/session-harness'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const LABEL_ZH = zhCN.signOut.label
const BUSY_ZH = zhCN.signOut.busy
const LABEL_EN = enUS.signOut.label

function makeScriptedHarness(
  extra: Parameters<typeof makeHarness>[0] = {},
): Harness {
  return makeHarness({
    [LOGIN_ANSWER]: () => authenticatedCompletion(),
    [PROFILE_GET]: () => principal(),
    ...extra,
  })
}

async function signIn(harness: Harness): Promise<void> {
  await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
}

function contrastRatio(a: string, b: string): number {
  const channel = (value: number): number => {
    const c = value / 255
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
  }
  const luminance = (hex: string): number => {
    const value = hex.replace('#', '')
    const r = Number.parseInt(value.slice(0, 2), 16)
    const g = Number.parseInt(value.slice(2, 4), 16)
    const b = Number.parseInt(value.slice(4, 6), 16)
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
  }
  const lighter = Math.max(luminance(a), luminance(b))
  const darker = Math.min(luminance(a), luminance(b))
  return (lighter + 0.05) / (darker + 0.05)
}

describe('SignOutButton', () => {
  it('default the button to an inheriting color, never the primary palette color', async () => {
    const harness = makeScriptedHarness()
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />)
    const button = screen.getByRole('button', { name: LABEL_ZH })
    expect(button).toHaveClass(buttonClasses.colorInherit)
    expect(button).not.toHaveClass(buttonClasses.colorPrimary)
    const { theme } = createAppTheme()
    const ratio = contrastRatio(
      theme.palette.primary.main,
      theme.palette.primary.contrastText,
    )
    expect(ratio).toBeGreaterThanOrEqual(4.5)
    expect(theme.palette.primary.main).not.toBe(theme.palette.primary.contrastText)
  })

  it('sign out on click, clearing the session token', async () => {
    const harness = makeScriptedHarness({ [LOGOUT]: () => undefined })
    await signIn(harness)
    expect(harness.store.get()).toBe('access-1')
    renderWithProviders(<SignOutButton session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() => expect(harness.store.get()).toBeNull())
    expect(harness.calls).toHaveLength(4)
    expect(harness.calls[3]?.method).toBe('POST')
    expect(harness.calls[3]?.path).toBe('/api/v1/auth/logout')
    expect(harness.calls[3]?.options?.body).toBeUndefined()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: LABEL_ZH }),
      ).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('keep focus on the button and refuse a second activation while the logout is in flight', async () => {
    let resolveLogout: (value: unknown) => void = () => {}
    const harness = makeScriptedHarness({
      [LOGOUT]: () =>
        new Promise((resolve) => {
          resolveLogout = resolve
        }),
    })
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    const busy = await screen.findByRole('button', { name: BUSY_ZH })
    expect(document.activeElement).toBe(busy)
    expect(busy).toHaveAttribute('aria-disabled', 'true')
    expect(busy).toHaveAttribute('aria-busy', 'true')
    expect(harness.calls).toHaveLength(4)
    await user.click(busy)
    expect(harness.calls).toHaveLength(4)
    await act(async () => {
      resolveLogout(undefined)
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: LABEL_ZH }),
      ).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('keep focus on the button when a refused logout lands', async () => {
    let rejectLogout: (error: unknown) => void = () => {}
    const harness = makeScriptedHarness({
      [LOGOUT]: () =>
        new Promise((_resolve, reject) => {
          rejectLogout = reject
        }),
    })
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />)
    const user = userEvent.setup()
    const idle = screen.getByRole('button', { name: LABEL_ZH })
    await user.click(idle)
    await screen.findByRole('button', { name: BUSY_ZH })
    await act(async () => {
      rejectLogout(apiError(503, 'authn.unavailable'))
    })
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    expect(document.activeElement).toBe(idle)
    expect(idle).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('render a refused logout in one alert: the local session still ends', async () => {
    const harness = makeScriptedHarness({
      [LOGOUT]: () => {
        throw apiError(503, 'authn.unavailable')
      },
    })
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    expect(harness.store.get()).toBeNull()
    expect(harness.calls).toHaveLength(4)
    expect(
      screen.getByRole('button', { name: LABEL_ZH }),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('clear the alert and drive another (idempotent) logout on a second click', async () => {
    const harness = makeScriptedHarness({ [LOGOUT]: () => undefined })
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() => expect(harness.store.get()).toBeNull())
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/logout'),
      ).toHaveLength(2),
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('re-render a failure text in the switched language', async () => {
    const harness = makeScriptedHarness({
      [LOGOUT]: () => {
        throw apiError(503, 'authn.unavailable')
      },
    })
    await signIn(harness)
    const { i18n } = renderWithProviders(
      <SignOutButton session={harness.session} />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(screen.getByRole('alert')).toHaveTextContent(
      enUS.errors.authn.unavailable,
    )
    expect(
      screen.getByRole('button', { name: LABEL_EN }),
    ).toBeInTheDocument()
  })

  it('render the en-US bundle on an English-starting instance', async () => {
    const harness = makeScriptedHarness({ [LOGOUT]: () => undefined })
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />, {
      language: 'en-US',
    })
    expect(
      screen.getByRole('button', { name: LABEL_EN }),
    ).toBeInTheDocument()
  })

  it('render the unknown fallback for a code outside the whitelist', async () => {
    const harness = makeScriptedHarness({
      [LOGOUT]: () => {
        throw apiError(500, 'authn.internal_error')
      },
    })
    await signIn(harness)
    renderWithProviders(<SignOutButton session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(zhCN.errors.unknown),
    )
  })

  it('pass axe with no violations after a failed logout', async () => {
    const harness = makeScriptedHarness({
      [LOGOUT]: () => {
        throw apiError(400, 'authn.bad_request')
      },
    })
    await signIn(harness)
    renderWithProviders(
      <div>
        <h1>Account</h1>
        <SignOutButton session={harness.session} />
      </div>,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: LABEL_ZH }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.bad_request,
      ),
    )
    await expectNoAxeViolations()
  })
})
