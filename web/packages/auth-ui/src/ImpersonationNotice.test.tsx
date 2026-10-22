/**
 * The impersonation notice's shared surface: the header chip, the account
 * menu's end item and the dismissal state that ties them to the banner.
 *
 * Covers the chip's disclosure contract (visible text as its name,
 * `aria-expanded` and `aria-controls`, toggling without moving focus), the
 * focus transfer a dismissal makes to the chip, the shared end attempt
 * (one call, disabled and busy while pending), and the dismissal reset
 * rules: a new grant, an identity change, an end, and a failed attempt
 * reset it; a re-read of the same grant does not.
 */

import { describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import MenuList from '@mui/material/MenuList'
import { I18nextProvider } from '@speed/i18n'
import { AppThemeProvider } from '@speed/ui-kit'
import {
  ImpersonationEndMenuItem,
  ImpersonationIndicator,
  ImpersonationNoticeBanner,
  ImpersonationNoticeIndicator,
  ImpersonationNoticeProvider,
  useImpersonationNotice,
} from './ImpersonationNotice.js'
import { createAuthUiI18n } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import type { ImpersonationBannerState } from './ImpersonationBanner.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const SUBJECT = 'Ada Lovelace'
const GRANT_ID = 'grant-1'

const IMPERSONATION: ImpersonationBannerState = {
  subject: SUBJECT,
  tenant: 'tenant-1',
  grantId: GRANT_ID,
}

interface HostProps {
  readonly impersonation: ImpersonationBannerState | null
  readonly identityKey?: string
  readonly pending?: boolean
  readonly failureCode?: string | null
  readonly onEnd?: () => void
  /** Renders the indicator in its icon-only form. */
  readonly compact?: boolean
}

/** A probe that exposes the region id and a dismissal action, so the
 * chip's aria-controls resolves and focus transfer is observable. */
function Probe(): ReactElement {
  const notice = useImpersonationNotice()
  if (notice === null) {
    throw new Error('expected the notice provider')
  }
  return (
    <>
      <div id={notice.regionId}>notice</div>
      <button type="button" onClick={notice.dismiss}>
        dismiss
      </button>
    </>
  )
}

/** The host's chip wiring: the controlled indicator over one provider. */
function Chip({
  compact = false,
}: {
  readonly compact?: boolean
} = {}): ReactElement | null {
  const notice = useImpersonationNotice()
  if (notice === null) {
    return null
  }
  return (
    <ImpersonationIndicator
      impersonation={notice.impersonation}
      expanded={!notice.dismissed}
      controls={notice.regionId}
      onToggle={notice.toggle}
      ref={notice.indicatorRef}
      compact={compact}
    />
  )
}

function Host({
  impersonation,
  identityKey,
  pending,
  failureCode = null,
  onEnd = () => {},
  compact = false,
}: HostProps) {
  return (
    <ImpersonationNoticeProvider
      impersonation={impersonation}
      identityKey={identityKey}
      pending={pending}
      failureCode={failureCode}
      onEnd={onEnd}
    >
      <h1>Page</h1>
      <Chip compact={compact} />
      <MenuList>
        <ImpersonationEndMenuItem />
      </MenuList>
      <Probe />
    </ImpersonationNoticeProvider>
  )
}

/** Renders the host inside one stable provider tree, so a rerender with
 * new props updates the host in place instead of remounting it. */
function renderHost(initial: HostProps) {
  const i18n = createAuthUiI18n()
  const tree = (props: HostProps): ReactElement => (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <Host {...props} />
      </AppThemeProvider>
    </I18nextProvider>
  )
  const result = render(tree(initial))
  return {
    ...result,
    rerenderHost: (props: HostProps) => result.rerender(tree(props)),
  }
}

describe('ImpersonationIndicator', () => {
  it('renders the chip with its visible text as the name and the disclosure attributes', () => {
    renderHost({ impersonation: IMPERSONATION })
    const chip = screen.getByRole('button', {
      name: zhCN.impersonation.impersonating,
    })
    expect(chip).toHaveAttribute('aria-expanded', 'true')
    const controls = chip.getAttribute('aria-controls')
    expect(controls).not.toBeNull()
    // The id the chip names is the visible notice's own region id.
    expect(document.getElementById(controls as string)).not.toBeNull()
  })

  it('renders the compact chip with the label as its name and no visible text', () => {
    renderHost({ impersonation: IMPERSONATION, compact: true })
    const chip = screen.getByRole('button', {
      name: zhCN.impersonation.impersonating,
    })
    // The rail has no room for the label; the name and the disclosure
    // attributes stay, so the control reads the same either way.
    expect(chip.textContent?.trim()).toBe('')
    expect(chip.querySelector('svg')).not.toBeNull()
    expect(chip).toHaveAttribute('aria-expanded', 'true')
    expect(chip).toHaveAttribute('aria-controls')
  })

  it('toggles the notice on activation and keeps focus on the chip', async () => {
    const user = userEvent.setup()
    renderHost({ impersonation: IMPERSONATION })
    const chip = screen.getByRole('button', {
      name: zhCN.impersonation.impersonating,
    })
    expect(chip).toHaveAttribute('aria-controls')
    await user.click(chip)
    expect(chip).toHaveAttribute('aria-expanded', 'false')
    // The banner body is unmounted while hidden, so the reference goes
    // with it.
    expect(chip).not.toHaveAttribute('aria-controls')
    expect(chip).toHaveFocus()
    await user.click(chip)
    expect(chip).toHaveAttribute('aria-expanded', 'true')
    expect(chip).toHaveAttribute('aria-controls')
    expect(chip).toHaveFocus()
  })

  it('describes the chip with the hide hint while expanded and the show hint while collapsed', async () => {
    const user = userEvent.setup()
    renderHost({ impersonation: IMPERSONATION })
    const chip = screen.getByRole('button', {
      name: zhCN.impersonation.impersonating,
    })
    await user.hover(chip)
    expect(
      await screen.findByRole('tooltip', {
        name: zhCN.impersonation.hideHint,
      }),
    ).toBeInTheDocument()

    // Collapse first, then hover again: the hint follows the state.
    await user.unhover(chip)
    await user.click(chip)
    await waitFor(() =>
      expect(chip).toHaveAttribute('aria-expanded', 'false'),
    )
    await user.hover(chip)
    expect(
      await screen.findByRole('tooltip', {
        name: zhCN.impersonation.showHint,
      }),
    ).toBeInTheDocument()
  })

  it('is the focus target a dismissal moves to', async () => {
    const user = userEvent.setup()
    renderHost({ impersonation: IMPERSONATION })
    await user.click(screen.getByRole('button', { name: 'dismiss' }))
    expect(
      screen.getByRole('button', {
        name: zhCN.impersonation.impersonating,
      }),
    ).toHaveFocus()
  })

  it('renders nothing while no impersonation is active', () => {
    renderHost({ impersonation: null })
    expect(
      screen.queryByRole('button', {
        name: zhCN.impersonation.impersonating,
      }),
    ).not.toBeInTheDocument()
  })

  it('renders nothing for a null impersonation, with no provider needed', () => {
    const i18n = createAuthUiI18n()
    render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ImpersonationIndicator
            impersonation={null}
            expanded
            onToggle={() => {}}
          />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    expect(
      screen.queryByRole('button', {
        name: zhCN.impersonation.impersonating,
      }),
    ).not.toBeInTheDocument()
  })
})

describe('ImpersonationEndMenuItem', () => {
  it('starts the shared attempt and names itself from the auth-ui bundle', async () => {
    const onEnd = vi.fn()
    const user = userEvent.setup()
    renderHost({ impersonation: IMPERSONATION, onEnd })
    await user.click(
      screen.getByRole('menuitem', { name: zhCN.impersonation.end }),
    )
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('is disabled and labelled busy while the shared attempt is in flight', () => {
    renderHost({ impersonation: IMPERSONATION, pending: true })
    expect(
      screen.getByRole('menuitem', { name: zhCN.impersonation.ending }),
    ).toHaveAttribute('aria-disabled', 'true')
  })

  it('renders nothing while no impersonation is active', () => {
    renderHost({ impersonation: null })
    expect(
      screen.queryByRole('menuitem', { name: zhCN.impersonation.end }),
    ).not.toBeInTheDocument()
  })
})

describe('provider-reading consumers', () => {
  interface BoundProps {
    readonly impersonation: ImpersonationBannerState | null
    readonly onEnd?: () => void
    readonly dismissible?: boolean
  }

  /** Renders the two provider-reading consumers under one provider. */
  function renderBound({
    impersonation,
    onEnd = () => {},
    dismissible,
  }: BoundProps) {
    const i18n = createAuthUiI18n()
    return render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ImpersonationNoticeProvider
            impersonation={impersonation}
            onEnd={onEnd}
          >
            <h1>Page</h1>
            <ImpersonationNoticeIndicator />
            <ImpersonationNoticeBanner dismissible={dismissible} />
          </ImpersonationNoticeProvider>
        </AppThemeProvider>
      </I18nextProvider>,
    )
  }

  const chip = (): HTMLElement =>
    screen.getByRole('button', { name: zhCN.impersonation.impersonating })

  it('render the banner and the chip from the provider’s state', () => {
    renderBound({ impersonation: IMPERSONATION })
    const region = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(region).toBeInTheDocument()
    expect(chip()).toHaveAttribute('aria-expanded', 'true')
    // The chip names the banner's own region.
    expect(chip()).toHaveAttribute('aria-controls', region.id)
  })

  it('dismiss the banner from its close control and move focus to the chip', async () => {
    const user = userEvent.setup()
    renderBound({ impersonation: IMPERSONATION })
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonation.hide }),
    )
    expect(
      screen.queryByRole('region', { name: zhCN.impersonation.regionLabel }),
    ).not.toBeInTheDocument()
    // The persistent indication survives the dismissal and takes focus.
    expect(chip()).toHaveAttribute('aria-expanded', 'false')
    expect(chip()).toHaveFocus()
  })

  it('omit the close control when the host makes the notice non-dismissible', () => {
    renderBound({ impersonation: IMPERSONATION, dismissible: false })
    expect(
      screen.queryByRole('button', { name: zhCN.impersonation.hide }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('region', { name: zhCN.impersonation.regionLabel }),
    ).toBeInTheDocument()
  })

  it('start the shared end attempt from the banner', async () => {
    const onEnd = vi.fn()
    const user = userEvent.setup()
    renderBound({ impersonation: IMPERSONATION, onEnd })
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    )
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('render nothing outside a provider', () => {
    const i18n = createAuthUiI18n()
    const { container } = render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ImpersonationNoticeIndicator />
          <ImpersonationNoticeBanner />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})

describe('dismissal reset', () => {
  const dismiss = async (): Promise<void> => {
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'dismiss' }))
  }

  const chip = (): HTMLElement =>
    screen.getByRole('button', { name: zhCN.impersonation.impersonating })

  it('resets on a new grant', async () => {
    const { rerenderHost } = renderHost({
      impersonation: IMPERSONATION,
      identityKey: 'account-1',
    })
    await dismiss()
    expect(chip()).toHaveAttribute('aria-expanded', 'false')

    rerenderHost({
      impersonation: { ...IMPERSONATION, grantId: 'grant-2' },
      identityKey: 'account-1',
    })
    expect(chip()).toHaveAttribute('aria-expanded', 'true')
  })

  it('resets on an identity change', async () => {
    const { rerenderHost } = renderHost({
      impersonation: IMPERSONATION,
      identityKey: 'account-1',
    })
    await dismiss()
    expect(chip()).toHaveAttribute('aria-expanded', 'false')

    rerenderHost({
      impersonation: IMPERSONATION,
      identityKey: 'account-2',
    })
    expect(chip()).toHaveAttribute('aria-expanded', 'true')
  })

  it('resets when the impersonation ends, so the next one starts visible', async () => {
    const { rerenderHost } = renderHost({
      impersonation: IMPERSONATION,
      identityKey: 'account-1',
    })
    await dismiss()
    rerenderHost({ impersonation: null, identityKey: 'account-1' })
    expect(
      screen.queryByRole('button', {
        name: zhCN.impersonation.impersonating,
      }),
    ).not.toBeInTheDocument()

    rerenderHost({
      impersonation: IMPERSONATION,
      identityKey: 'account-1',
    })
    expect(chip()).toHaveAttribute('aria-expanded', 'true')
  })

  it('resets when the shared attempt reports a failure', async () => {
    const { rerenderHost } = renderHost({ impersonation: IMPERSONATION })
    await dismiss()
    expect(chip()).toHaveAttribute('aria-expanded', 'false')

    rerenderHost({
      impersonation: IMPERSONATION,
      failureCode: 'authn.unavailable',
    })
    expect(chip()).toHaveAttribute('aria-expanded', 'true')
  })

  it('has no axe violations with the chip and the account-menu item', async () => {
    renderHost({ impersonation: IMPERSONATION })
    await expectNoAxeViolations()
  })

  it('keeps the dismissal through a re-read of the same grant', async () => {
    const { rerenderHost } = renderHost({
      impersonation: IMPERSONATION,
      identityKey: 'account-1',
    })
    await dismiss()
    // A status poll / focus re-read hands the same grant again.
    rerenderHost({
      impersonation: { ...IMPERSONATION },
      identityKey: 'account-1',
    })
    expect(chip()).toHaveAttribute('aria-expanded', 'false')
  })

  it('does not reset when the grant id arrives after the dismissal', async () => {
    // The dismissal was made while the status read was still pending.
    // Only a change between two KNOWN grant ids is a new impersonation;
    // the absent-to-known edge is the same grant filling in.
    const { rerenderHost } = renderHost({
      impersonation: { subject: SUBJECT },
      identityKey: 'account-1',
    })
    await dismiss()
    expect(chip()).toHaveAttribute('aria-expanded', 'false')

    rerenderHost({
      impersonation: { subject: SUBJECT, grantId: GRANT_ID },
      identityKey: 'account-1',
    })
    expect(chip()).toHaveAttribute('aria-expanded', 'false')
  })
})
