import { useState } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { alertClasses } from '@mui/material/Alert'
import { I18nextProvider } from '@speed/i18n'
import {
  AppThemeProvider,
  createAppTheme,
  DisplayTimeZoneProvider,
} from '@speed/ui-kit'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import {
  ImpersonationBanner,
  type ImpersonationBannerState,
} from './ImpersonationBanner.js'
import {
  createAuthUiI18n,
  renderWithProviders,
} from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const SUBJECT = 'Ada Lovelace'
const TENANT = 'tenant-1'
const EXPIRES_AT = new Date('2026-10-18T12:30:00Z')

const ACTING_AS_ZH = zhCN.impersonation.actingAs.replace(
  '{{subject}}',
  SUBJECT,
)
const TENANT_ZH = zhCN.impersonation.tenant.replace('{{tenant}}', TENANT)
const EXPIRY_ZH = zhCN.impersonation.expiresAt.replace(
  '{{expiresAt}}',
  new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'long',
    timeZone: TEST_TIME_ZONE,
  }).format(EXPIRES_AT),
)

function labelBeforeValue(template: string): string {
  const placeholder = template.indexOf('{{')
  return placeholder < 0 ? template : template.slice(0, placeholder)
}

const TENANT_LABEL_ZH = labelBeforeValue(zhCN.impersonation.tenant)
const EXPIRY_LABEL_ZH = labelBeforeValue(zhCN.impersonation.expiresAt)

function isVisuallyHidden(element: Element): boolean {
  for (
    let node: Element | null = element;
    node !== null;
    node = node.parentElement
  ) {
    const style = getComputedStyle(node)
    if (style.position !== 'absolute') {
      continue
    }
    const clipped =
      (style.clip !== '' && style.clip !== 'auto') ||
      (style.clipPath !== '' && style.clipPath !== 'none')
    if (clipped) {
      return true
    }
  }
  return false
}

function messageSlotDeclaration(
  owner: Element,
  property: string,
): string | null {
  const ownerClasses = Array.from(owner.classList)
  for (const rule of emittedStyleText().split('}')) {
    const open = rule.indexOf('{')
    if (open < 0) {
      continue
    }
    const selector = rule.slice(0, open).trim()
    const suffix = ` .${alertClasses.message}`
    if (!selector.endsWith(suffix)) {
      continue
    }
    const ancestors = selector.slice(0, -suffix.length).split(/\s+/)
    if (!ownerClasses.some((name) => ancestors.includes(`.${name}`))) {
      continue
    }
    const declarations = rule.slice(open + 1)
    const match = new RegExp(
      `(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`,
    ).exec(declarations)
    if (match?.[1] !== undefined) {
      return match[1].trim()
    }
  }
  return null
}

function region(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label]')
}

describe('ImpersonationBanner', () => {
  it('renders only one empty live region while no impersonation is active', () => {
    renderWithProviders(
      <ImpersonationBanner impersonation={null} onEnd={() => {}} />,
    )
    const liveRegions = screen.getAllByRole('status')
    expect(liveRegions).toHaveLength(1)
    expect(liveRegions[0]).toHaveTextContent('')
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('announces the impersonation and states subject, tenant, expiry and the management notice', async () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{
          subject: SUBJECT,
          tenant: TENANT,
          expiresAt: EXPIRES_AT,
        }}
        onEnd={() => {}}
      />,
    )
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('')
    await waitFor(() => expect(status).toHaveTextContent(ACTING_AS_ZH))

    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(notice).toHaveTextContent(ACTING_AS_ZH)
    expect(notice).toHaveTextContent(TENANT_ZH)
    expect(notice).toHaveTextContent(EXPIRY_ZH)
    expect(notice).toHaveTextContent(
      zhCN.impersonation.managementUnavailable,
    )
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    ).toBeInTheDocument()
  })

  it('paints the notice with the warning role fill and its own label color', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    const { palette } = createAppTheme().theme
    expect(notice).toHaveStyle({
      backgroundColor: palette.warning.main,
      color: palette.warning.contrastText,
    })
  })

  it('render the expiry in the host’s display time zone', () => {
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const formatter = (timeZone: string): string =>
      new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'long',
        timeZone,
      }).format(EXPIRES_AT)
    expect(formatter('Asia/Tokyo')).not.toBe(formatter(browserZone))
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Asia/Tokyo">
        <ImpersonationBanner
          impersonation={{ subject: SUBJECT, expiresAt: EXPIRES_AT }}
          onEnd={() => {}}
        />
      </DisplayTimeZoneProvider>,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(notice).toHaveTextContent(
      zhCN.impersonation.expiresAt.replace(
        '{{expiresAt}}',
        formatter('Asia/Tokyo'),
      ),
    )
    expect(notice).not.toHaveTextContent(
      zhCN.impersonation.expiresAt.replace(
        '{{expiresAt}}',
        formatter(browserZone),
      ),
    )
  })

  it('names the zone on the operator authorization expiry', () => {
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const longFormatter = (timeZone: string): string =>
      new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'long',
        timeZone,
      }).format(EXPIRES_AT)
    expect(longFormatter('Europe/Berlin')).not.toBe(
      longFormatter(browserZone),
    )
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Europe/Berlin">
        <ImpersonationBanner
          impersonation={{ subject: SUBJECT, expiresAt: EXPIRES_AT }}
          onEnd={() => {}}
        />
      </DisplayTimeZoneProvider>,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(notice).toHaveTextContent(
      zhCN.impersonation.expiresAt.replace(
        '{{expiresAt}}',
        longFormatter('Europe/Berlin'),
      ),
    )
    expect(notice).not.toHaveTextContent(
      zhCN.impersonation.expiresAt.replace(
        '{{expiresAt}}',
        longFormatter(browserZone),
      ),
    )
  })

  it('announces the start when an impersonation appears after mount', async () => {
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>(null)
      return (
        <>
          <button
            type="button"
            onClick={() => {
              setImpersonation({ subject: SUBJECT })
            }}
          >
            start
          </button>
          <ImpersonationBanner
            impersonation={impersonation}
            onEnd={() => {}}
          />
        </>
      )
    }
    renderWithProviders(<Host />)
    expect(screen.getByRole('status')).toHaveTextContent('')
    await userEvent.setup().click(screen.getByRole('button', { name: 'start' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH),
    )
  })

  it('mounts the live region empty on the first commit and fills it afterwards', () => {
    const i18n = createAuthUiI18n()
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ImpersonationBanner
            impersonation={{ subject: SUBJECT }}
            onEnd={() => {}}
          />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    expect(markup).toContain('role="status"')
    const [before = '', after = ''] = zhCN.impersonation.actingAs.split(
      '{{subject}}',
    )
    expect(markup.split(before)).toHaveLength(2)
    expect(markup.split(after)).toHaveLength(2)
    expect(markup.split(`>${SUBJECT}<`)).toHaveLength(2)
  })

  it('announces an impersonation that is already current on first mount', async () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('')
    await waitFor(() => expect(status).toHaveTextContent(ACTING_AS_ZH))
  })

  it('keeps the end control usable when tenant and expiry are unknown', async () => {
    const onEnd = vi.fn()
    const unread = { subject: SUBJECT, expiresAt: null as unknown as Date }
    renderWithProviders(
      <ImpersonationBanner impersonation={unread} onEnd={onEnd} />,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(notice).not.toHaveTextContent(TENANT_LABEL_ZH)
    expect(notice).not.toHaveTextContent(EXPIRY_LABEL_ZH)
    expect(notice).toHaveTextContent(ACTING_AS_ZH)

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.impersonation.end }))
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('renders the banner for a malformed expiry instead of throwing out of Intl', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT, expiresAt: new Date('nope') }}
        onEnd={() => {}}
      />,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(notice).toHaveTextContent(ACTING_AS_ZH)
    expect(notice).not.toHaveTextContent(EXPIRY_LABEL_ZH)
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    expect(end).not.toHaveAttribute('aria-disabled', 'true')
    expect(end).toBeEnabled()
  })

  it('falls back to the browser zone when the host’s display time zone is not an IANA name', () => {
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(browserZone).not.toBe('UTC')
    const browserExpiry = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'long',
      timeZone: browserZone,
    }).format(EXPIRES_AT)
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Asia/Shangai">
        <ImpersonationBanner
          impersonation={{ subject: SUBJECT, expiresAt: EXPIRES_AT }}
          onEnd={() => {}}
        />
      </DisplayTimeZoneProvider>,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(notice).toHaveTextContent(ACTING_AS_ZH)
    expect(notice).toHaveTextContent(
      zhCN.impersonation.expiresAt.replace('{{expiresAt}}', browserExpiry),
    )
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    expect(end).not.toHaveAttribute('aria-disabled', 'true')
    expect(end).toBeEnabled()
  })

  it('keeps the end control focusable and reports it unavailable and busy while the shared attempt is in flight', async () => {
    const onEnd = vi.fn()
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={onEnd}
        pending
      />,
    )
    const ending = screen.getByRole('button', {
      name: zhCN.impersonation.ending,
    })
    expect(ending).not.toHaveAttribute('disabled')
    expect(ending).toHaveAttribute('aria-disabled', 'true')
    expect(ending).toHaveAttribute('aria-busy', 'true')
    ending.focus()
    expect(ending).toHaveFocus()
    await userEvent.setup().click(ending)
    expect(onEnd).not.toHaveBeenCalled()
  })

  it('renders the shared attempt failure inside the banner and leaves the control retryable', async () => {
    const onEnd = vi.fn()
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={onEnd}
        failureCode="authn.unavailable"
      />,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    const alert = within(notice).getByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    expect(end).not.toHaveAttribute('aria-disabled', 'true')
    expect(end).toBeEnabled()
    await userEvent.setup().click(end)
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('announces and shows the shared attempt success, and the text outlives the banner', async () => {
    const { rerender } = renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
        successMessage={zhCN.impersonation.ended}
      />,
    )
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('')
    await waitFor(() =>
      expect(status).toHaveTextContent(zhCN.impersonation.ended),
    )
    expect(isVisuallyHidden(status)).toBe(false)

    rerender(
      <ImpersonationBanner
        impersonation={null}
        onEnd={() => {}}
        successMessage={zhCN.impersonation.ended}
      />,
    )
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
  })

  it('announces an end that settles outside the banner, without a success message', async () => {
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <>
          <button type="button" onClick={() => setImpersonation(null)}>
            end elsewhere
          </button>
          <ImpersonationBanner
            impersonation={impersonation}
            onEnd={() => {}}
          />
        </>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH),
    )
    await user.click(screen.getByRole('button', { name: 'end elsewhere' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
  })

  it('lands focus on the settlement when the end control that started it unmounts', async () => {
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <ImpersonationBanner
          impersonation={impersonation}
          onEnd={() => setImpersonation(null)}
        />
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    end.focus()
    expect(end).toHaveFocus()

    await user.click(end)

    expect(
      screen.queryByRole('region', {
        name: zhCN.impersonation.regionLabel,
      }),
    ).not.toBeInTheDocument()
    const status = screen.getByRole('status')
    const settlement = status.closest('.MuiAlert-root')?.parentElement
    expect(settlement).not.toBeNull()
    await waitFor(() => expect(settlement).toHaveFocus())
    expect(document.activeElement).not.toBe(document.body)
  })

  it('leaves focus alone when the operator moved it before the impersonation ended', async () => {
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <>
          <ImpersonationBanner
            impersonation={impersonation}
            onEnd={() => {}}
          />
          <button type="button" onClick={() => setImpersonation(null)}>
            end elsewhere
          </button>
        </>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    end.focus()
    expect(end).toHaveFocus()

    const elsewhere = screen.getByRole('button', { name: 'end elsewhere' })
    await user.click(elsewhere)

    expect(
      screen.queryByRole('region', {
        name: zhCN.impersonation.regionLabel,
      }),
    ).not.toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
    expect(elsewhere).toHaveFocus()
  })

  it('replaces a standing end confirmation when a new impersonation appears', async () => {
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <>
          <button type="button" onClick={() => setImpersonation(null)}>
            clear
          </button>
          <button
            type="button"
            onClick={() => setImpersonation({ subject: 'Bob' })}
          >
            start again
          </button>
          <ImpersonationBanner
            impersonation={impersonation}
            onEnd={() => {}}
          />
        </>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    await user.click(screen.getByRole('button', { name: 'clear' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )

    await user.click(screen.getByRole('button', { name: 'start again' }))
    const status = screen.getByRole('status')
    await waitFor(() =>
      expect(status).toHaveTextContent(
        zhCN.impersonation.actingAs.replace('{{subject}}', 'Bob'),
      ),
    )
    expect(status).not.toHaveTextContent(zhCN.impersonation.ended)
  })

  it('keeps the hidden announcement region from becoming a scroll container', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    const status = screen.getByRole('status')
    const outer = status.closest('.MuiAlert-root')?.parentElement
    expect(outer).not.toBeNull()
    expect(messageSlotDeclaration(outer as Element, 'overflow')).toBe(
      'hidden',
    )
  })

  it('keeps the announcement clipped to the hidden box while impersonating', async () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    const status = screen.getByRole('status')
    await waitFor(() => expect(status).toHaveTextContent(ACTING_AS_ZH))
    expect(isVisuallyHidden(status)).toBe(true)
  })

  it('unmounts the visible notice on dismissal while the live region stays', async () => {
    const { container } = renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
        dismissed
        onDismiss={() => {}}
        regionId="impersonation-notice"
      />,
    )
    expect(region(container)).toBeNull()
    expect(document.getElementById('impersonation-notice')).toBeNull()
    expect(
      screen.queryByRole('region', {
        name: zhCN.impersonation.regionLabel,
      }),
    ).not.toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH),
    )
  })

  it('announces the end of a dismissed notice too', async () => {
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <>
          <button type="button" onClick={() => setImpersonation(null)}>
            end elsewhere
          </button>
          <ImpersonationBanner
            impersonation={impersonation}
            onEnd={() => {}}
            dismissed
            onDismiss={() => {}}
          />
        </>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH),
    )
    await user.click(screen.getByRole('button', { name: 'end elsewhere' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
  })

  it('names the close control and reports its activation', async () => {
    const onDismiss = vi.fn()
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
        onDismiss={onDismiss}
      />,
    )
    const close = screen.getByRole('button', {
      name: zhCN.impersonation.hide,
    })
    await userEvent.setup().click(close)
    expect(onDismiss).toHaveBeenCalledTimes(1)
  })

  it('renders no close control when the host does not bind a dismissal', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.impersonation.hide }),
    ).not.toBeInTheDocument()
  })

  it('caps a long tenant identifier on one line, carrying the full value as a title', () => {
    const longTenant = 'a-very-long-tenant-identifier-that-must-not-grow'
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT, tenant: longTenant }}
        onEnd={() => {}}
      />,
    )
    const tenant = screen.getByText(
      zhCN.impersonation.tenant.replace('{{tenant}}', longTenant),
    )
    expect(tenant).toHaveAttribute('title', longTenant)
    expect(tenant).toHaveStyle({
      whiteSpace: 'nowrap',
      textOverflow: 'ellipsis',
      overflow: 'hidden',
    })
  })

  it('caps the acting-as subject so a display name cannot grow the bar', () => {
    const longSubject = 'Alexandria Konstantinopoulos'
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{
          subject: longSubject,
          tenant: TENANT,
          expiresAt: EXPIRES_AT,
        }}
        onEnd={() => {}}
        onDismiss={() => {}}
      />,
    )
    expect(
      screen.getByRole('region', { name: zhCN.impersonation.regionLabel }),
    ).toHaveTextContent(
      zhCN.impersonation.actingAs.replace('{{subject}}', longSubject),
    )
    const subject = screen.getByText(longSubject)
    expect(subject).toHaveAttribute('title', longSubject)
    expect(getComputedStyle(subject)).toMatchObject({
      display: 'inline-block',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
      verticalAlign: 'bottom',
    })
    expect(emittedStyleText()).toMatch(/max-width:\s*min\(16ch,\s*100%\)/)

    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(getComputedStyle(notice).paddingLeft).toBe('8px')
    expect(getComputedStyle(notice).columnGap).toBe('4px')
  })

  it('flows the facts as one inline paragraph, the controls at the row end', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{
          subject: SUBJECT,
          tenant: TENANT,
          expiresAt: EXPIRES_AT,
        }}
        onEnd={() => {}}
        onDismiss={() => {}}
      />,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(getComputedStyle(notice).flexWrap).toBe('wrap')
    expect(getComputedStyle(notice).paddingTop).toBe('4px')
    expect(getComputedStyle(notice).paddingBottom).toBe('4px')
    expect(getComputedStyle(notice).minHeight).toBe('40px')

    const facts = notice.firstElementChild as HTMLElement | null
    expect(facts?.tagName).toBe('P')
    expect(getComputedStyle(facts as HTMLElement).flexBasis).toBe('0px')
    expect(getComputedStyle(facts as HTMLElement).flexGrow).toBe('1')
    const statements = within(facts as HTMLElement)
    const statementTexts = [
      TENANT_ZH,
      EXPIRY_ZH,
      zhCN.impersonation.managementUnavailable,
    ]
    for (const text of statementTexts) {
      const node = statements.getByText(text)
      expect(getComputedStyle(node).display).not.toBe('block')
    }
    const subject = statements.getByText(SUBJECT)
    expect(getComputedStyle(subject).display).toBe('inline-block')
    expect(
      getComputedStyle(subject.parentElement as HTMLElement).display,
    ).not.toBe('block')
    expect(getComputedStyle(statements.getByText(TENANT_ZH)).display).toBe(
      'inline-block',
    )

    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    const close = screen.getByRole('button', { name: zhCN.impersonation.hide })
    expect(end.parentElement).toBe(close.parentElement)
    const controls = end.parentElement as HTMLElement
    expect(controls.parentElement).toBe(notice)
    expect(getComputedStyle(controls).marginLeft).not.toBe('auto')

    const emotionClass = Array.from(controls.classList).find((name) =>
      name.startsWith('css-'),
    )
    expect(emotionClass).toBeDefined()
    const escaped = (emotionClass as string).replace(
      /[.*+?^${}()|[\]\\]/g,
      '\\$&',
    )
    const belowXs = new RegExp(
      `@media \\(max-width:599\\.95px\\)\\{\\.${escaped}\\{([^}]*)\\}\\}`,
    ).exec(emittedStyleText())
    expect(belowXs?.[1]).toContain('flex-basis:100%')
  })

  function ClearableHost() {
    const [impersonation, setImpersonation] =
      useState<ImpersonationBannerState | null>({ subject: SUBJECT })
    return (
      <>
        <button type="button" onClick={() => setImpersonation(null)}>
          clear
        </button>
        <ImpersonationBanner
          impersonation={impersonation}
          onEnd={async () => {}}
        />
      </>
    )
  }

  it('confirms the end while the banner is still mounted, and the confirmation outlives an explicit clear', async () => {
    const user = userEvent.setup()
    renderWithProviders(<ClearableHost />)
    await user.click(screen.getByRole('button', { name: 'clear' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
  })

  it('passes axe with no violations while the banner is shown', async () => {
    renderWithProviders(
      <div>
        <h1>Page</h1>
        <ImpersonationBanner
          impersonation={{
            subject: SUBJECT,
            tenant: TENANT,
            expiresAt: EXPIRES_AT,
          }}
          onEnd={() => {}}
          onDismiss={() => {}}
          regionId="impersonation-notice"
        />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('passes axe with no violations while the notice is dismissed', async () => {
    renderWithProviders(
      <div>
        <h1>Page</h1>
        <ImpersonationBanner
          impersonation={{ subject: SUBJECT }}
          onEnd={() => {}}
          dismissed
          onDismiss={() => {}}
          regionId="impersonation-notice"
        />
      </div>,
    )
    await expectNoAxeViolations()
  })
})
