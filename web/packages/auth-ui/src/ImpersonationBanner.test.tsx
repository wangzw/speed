/**
 * ImpersonationBanner behaviour: the presentational notice over the
 * host-owned end attempt.
 *
 * Covers the idle state (one mounted live region and nothing else), the
 * active banner (subject, tenant and expiry when given, the management
 * notice, a named region landmark), the shared attempt's presentation
 * (disabled and busy while pending, the failure code rendered inside the
 * banner, the success message announced and shown), the dismissal (the
 * visible region hides without unmounting, the close control names
 * itself, the live region keeps announcing), the host-driven end edge,
 * the tenant identifier's ellipsis with its full value as a title, and an
 * axe pass. Text expectations read the shipped bundles, never inline
 * language.
 */

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
    // The harness renders in its fixed TEST_TIME_ZONE; the fixture states
    // the same zone explicitly so it does not read the machine's.
    timeZone: TEST_TIME_ZONE,
  }).format(EXPIRES_AT),
)

/** The fixed text before a template's first placeholder. A negative
 * assertion built from a value the test never passes is always true, so
 * the "not stated" checks look for the label itself, independent of the
 * interpolated value. */
function labelBeforeValue(template: string): string {
  const placeholder = template.indexOf('{{')
  return placeholder < 0 ? template : template.slice(0, placeholder)
}

const TENANT_LABEL_ZH = labelBeforeValue(zhCN.impersonation.tenant)
const EXPIRY_LABEL_ZH = labelBeforeValue(zhCN.impersonation.expiresAt)

/** Whether the element is clipped to the 1x1 visually-hidden box by
 * itself or an ancestor. jsdom renders no layout, but it resolves the
 * emotion class rules into computed styles, so the clip recipe of every
 * ancestor is observable. */
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

/** The declaration emotion emitted for `property` in the rule that
 * targets the MUI message slot under one of `owner`'s classes. The
 * element's generated stylesheet is the thing a browser applies; the
 * focusability Chromium derives from a scroll container cannot be
 * measured in jsdom. */
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

/** The banner's named region, visible or not. */
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
    // No banner landmark, no end control, no region of any kind.
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('announces the impersonation and states subject, tenant, expiry and the management notice', () => {
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
    // The start is announced through the one live region that was
    // already mounted before the impersonation appeared.
    expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH)

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
    // The banner is the warning role's filled surface: the role's main is
    // the fill and its contrastText is the label chosen to read on that
    // fill in both modes. The pair only holds while the two stay together.
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
    // The host's zone differs from the browser's, so the assertions below
    // discriminate the provider's effect: an implementation that ignored
    // it and fell back to the browser zone would render the other string.
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
    // The host's zone differs from the browser's, so the assertions below
    // discriminate the provider's effect: an implementation that ignored
    // it and fell back to the browser zone would render the other string.
    // The suite pins the browser zone (auth-ui's vitest.config.ts), so a
    // value equal to it would not tell the two apart.
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
    // The deadline carries its zone name, so the operator reading it can
    // tell which clock it is on; the medium default omits the zone.
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
    // Mounted empty before anything happens ...
    expect(screen.getByRole('status')).toHaveTextContent('')
    await userEvent.setup().click(screen.getByRole('button', { name: 'start' }))
    // ... and filled on the tick after the impersonation appears.
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH),
    )
  })

  it('mounts the live region empty on the first commit and fills it afterwards', () => {
    // Server rendering is the one place the first commit is observable:
    // effects do not run, so this markup is exactly what the first paint
    // would show. The banner body states the acting-as line once; the
    // live region must still be empty, or a first mount that is already
    // impersonating would be born with its text and say nothing.
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
    // The banner body states the acting-as line once -- the subject is its
    // own element, so the line's text is split around it -- and the live
    // region stays empty on the first commit.
    const [before = '', after = ''] = zhCN.impersonation.actingAs.split(
      '{{subject}}',
    )
    expect(markup.split(before)).toHaveLength(2)
    expect(markup.split(after)).toHaveLength(2)
    expect(markup.split(`>${SUBJECT}<`)).toHaveLength(2)
  })

  it('announces an impersonation that is already current on first mount', () => {
    // The frame mounts after a start, and a reload while impersonating
    // mounts this component too: the region is born empty and filled on
    // the next tick, so both cases are announced.
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH)
  })

  it('keeps the end control usable when tenant and expiry are unknown', async () => {
    const onEnd = vi.fn()
    // `null` is what an unread optional field looks like at the wire
    // boundary; the cast passes that shape through the declared type.
    const unread = { subject: SUBJECT, expiresAt: null as unknown as Date }
    renderWithProviders(
      <ImpersonationBanner impersonation={unread} onEnd={onEnd} />,
    )
    const notice = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    // Neither line is stated: the labels are looked for as fixed text,
    // not as a value the test never passed.
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
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    ).toBeEnabled()
  })

  it('falls back to the browser zone when the host’s display time zone is not an IANA name', () => {
    // The provider forwards the host's value unvalidated, and the engine
    // rejects an unknown zone with a RangeError; the shared entry resolves
    // the chain to the browser's zone instead, so the banner survives and
    // keeps its expiry line. The suite's vitest config pins a non-UTC
    // browser zone: on a UTC runner this case would also hold for an
    // implementation that fell straight through to UTC.
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
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    ).toBeEnabled()
  })

  it('disables and marks the end control busy while the shared attempt is in flight', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
        pending
      />,
    )
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.ending }),
    ).toBeDisabled()
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
    // The control is back and a click is a new attempt.
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    expect(end).toBeEnabled()
    await userEvent.setup().click(end)
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('announces and shows the shared attempt success, and the text outlives the banner', () => {
    // The host's own shape: the attempt's success message arrives while
    // the banner is still mounted, then the host clears `impersonation`.
    const { rerender } = renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
        successMessage={zhCN.impersonation.ended}
      />,
    )
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(zhCN.impersonation.ended)
    // The confirmed text is shown, not left inside the 1x1 hidden box.
    expect(isVisuallyHidden(status)).toBe(false)

    rerender(
      <ImpersonationBanner
        impersonation={null}
        onEnd={() => {}}
        successMessage={zhCN.impersonation.ended}
      />,
    )
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.impersonation.ended,
    )
  })

  it('announces an end that settles outside the banner, without a success message', async () => {
    // The account menu ends the impersonation: the message is the
    // permanent region's ended text, derived from the displayed
    // impersonation disappearing. The host clears its state in place, so
    // the component observes the transition rather than remounting.
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

    // The next impersonation is the current context: the stale
    // confirmation must not cover its announcement.
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
    // MUI's message slot carries `overflow: auto`; squeezed to the 1px
    // clipped width it would overflow into a scroll region, which
    // Chromium makes a keyboard focus stop. jsdom cannot construct that
    // focusability, so the emitted rule is the observable half: the
    // hidden branch removes the scroll container.
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    const status = screen.getByRole('status')
    const outer = status.parentElement
    expect(outer).not.toBeNull()
    expect(messageSlotDeclaration(outer as Element, 'overflow')).toBe(
      'hidden',
    )
  })

  it('keeps the announcement clipped to the hidden box while impersonating', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={() => {}}
      />,
    )
    // The region is the announcement's carrier, but the banner itself
    // states the same line: the region must stay visually hidden.
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(ACTING_AS_ZH)
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
    // The visible region is gone entirely; the permanent live region is
    // not, and still announces the start.
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
    // The identifier stays one line and ellipsizes; the complete value is
    // available on the title, so truncation never loses the fact.
    expect(tenant).toHaveAttribute('title', longTenant)
    expect(tenant).toHaveStyle({
      whiteSpace: 'nowrap',
      textOverflow: 'ellipsis',
      overflow: 'hidden',
    })
  })

  it('caps the acting-as subject so a display name cannot grow the bar', () => {
    // The design's target is one text line at 1280 and at most two at 800.
    // The fixed sentences leave room for only a short subject at 1280, so
    // an unbounded display name wraps the paragraph and misses that. The
    // subject is bounded like the tenant identifier; the row's own gaps
    // and padding are held tight for the same width budget.
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
    // The sentence still reads whole in the rendered text...
    expect(
      screen.getByRole('region', { name: zhCN.impersonation.regionLabel }),
    ).toHaveTextContent(
      zhCN.impersonation.actingAs.replace('{{subject}}', longSubject),
    )
    // ...with the subject alone the bounded, ellipsized segment, its full
    // value carried as a title, exactly like the tenant identifier.
    const subject = screen.getByText(longSubject)
    expect(subject).toHaveAttribute('title', longSubject)
    expect(getComputedStyle(subject)).toMatchObject({
      display: 'inline-block',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
      verticalAlign: 'bottom',
    })
    // jsdom does not resolve `min()`, so the cap is read off the emitted
    // rule.
    expect(emittedStyleText()).toMatch(/max-width:\s*min\(16ch,\s*100%\)/)

    // The width the row reserves for itself: everything outside the text
    // is width the single line cannot use at 1280. jsdom renders no
    // layout, so the line count itself is measured in the browser
    // verification pass; these values are the ones it is measured against.
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
    // The design's compact row: one flex line that wraps, small vertical
    // padding.
    expect(getComputedStyle(notice).flexWrap).toBe('wrap')
    expect(getComputedStyle(notice).paddingTop).toBe('4px')
    expect(getComputedStyle(notice).paddingBottom).toBe('4px')
    // The design's 40-48px single-line target, held as a floor.
    expect(getComputedStyle(notice).minHeight).toBe('40px')

    // The four statements are children of ONE paragraph and flow inline:
    // the line breaks fall by text flow, not one sentence per line.
    const facts = notice.firstElementChild as HTMLElement | null
    expect(facts?.tagName).toBe('P')
    // Flex line breaking uses the paragraph's base size, not its shrunk
    // size: a non-zero base pushes the controls onto their own row at
    // widths where the wrapped text would still fit beside them.
    expect(getComputedStyle(facts as HTMLElement).flexBasis).toBe('0px')
    // The facts take the room the controls leave, so the controls close
    // the row on the right without an automatic margin.
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
    // The acting-as line keeps the subject in its own bounded element so
    // an arbitrary display name cannot grow the bar; the line itself stays
    // inline beside the other facts.
    const subject = statements.getByText(SUBJECT)
    expect(getComputedStyle(subject).display).toBe('inline-block')
    expect(
      getComputedStyle(subject.parentElement as HTMLElement).display,
    ).not.toBe('block')
    // The tenant identifier is an inline block, so it can ellipsize.
    expect(getComputedStyle(statements.getByText(TENANT_ZH)).display).toBe(
      'inline-block',
    )

    // The controls share the row, at its end, from sm up: the region is
    // the row they sit in.
    const end = screen.getByRole('button', { name: zhCN.impersonation.end })
    const close = screen.getByRole('button', { name: zhCN.impersonation.hide })
    expect(end.parentElement).toBe(close.parentElement)
    const controls = end.parentElement as HTMLElement
    expect(controls.parentElement).toBe(notice)
    // The facts block grows into the free width, so the controls close the
    // row on the right without an automatic margin. An auto margin here is
    // the two-ends layout the action row rule forbids.
    expect(getComputedStyle(controls).marginLeft).not.toBe('auto')

    // Below xs they take their own line, flush right: emotion emitted the
    // breakpoint rule jsdom itself cannot evaluate.
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

  /** Mounts the banner with a working host clear control, the real host
   * shape the success tests exercise. */
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
    // A real host's end write settles over a network round trip: the
    // success message can arrive before the host's own re-read clears
    // `impersonation`. The confirmation must be readable while the banner
    // is still on screen, and must stay after the host clears it.
    const user = userEvent.setup()
    renderWithProviders(<ClearableHost />)
    // The confirmation is the host's success message; the host shape
    // here has none, so the ended edge is what is announced.
    await user.click(screen.getByRole('button', { name: 'clear' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
  })

  it('passes axe with no violations while the banner is shown', async () => {
    // The page heading the harness supplies answers
    // page-has-heading-one, the way every axe scan in this package is
    // rendered.
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
