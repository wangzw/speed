/**
 * ImpersonationBanner behaviour: the presentational banner and the one
 * end control over a host-bound operation.
 *
 * Covers the idle state (one mounted live region and nothing else), the
 * active banner (subject, tenant and expiry when given, the management
 * notice, a named region landmark), the end attempt (one call through
 * the write-feedback scope, disabled and busy in flight, a confirmation
 * that outlives the banner, a failure rendered inside the banner), the
 * bare-404 case the component deliberately does not interpret, and an
 * axe pass. Text expectations read the shipped bundles, never inline
 * language.
 */

import { useState } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from '@speed/i18n'
import { AppThemeProvider } from '@speed/ui-kit'
import { apiError } from '@speed/test-utils/session-harness'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
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
    timeStyle: 'short',
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
    const suffix = ' .MuiAlert-message'
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

describe('ImpersonationBanner', () => {
  it('renders only one empty live region while no impersonation is active', () => {
    renderWithProviders(
      <ImpersonationBanner impersonation={null} onEnd={async () => {}} />,
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
        onEnd={async () => {}}
      />,
    )
    // The start is announced through the one live region that was
    // already mounted before the impersonation appeared.
    expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH)

    const region = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(region).toHaveTextContent(ACTING_AS_ZH)
    expect(region).toHaveTextContent(TENANT_ZH)
    expect(region).toHaveTextContent(EXPIRY_ZH)
    expect(region).toHaveTextContent(
      zhCN.impersonation.managementUnavailable,
    )
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    ).toBeInTheDocument()
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
            onEnd={async () => {}}
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
            onEnd={async () => {}}
          />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    expect(markup).toContain('role="status"')
    expect(markup.split(ACTING_AS_ZH)).toHaveLength(2)
  })

  it('announces an impersonation that is already current on first mount', () => {
    // The frame mounts after a start, and a reload while impersonating
    // mounts this component too: the region is born empty and filled on
    // the next tick, so both cases are announced.
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={async () => {}}
      />,
    )
    expect(screen.getByRole('status')).toHaveTextContent(ACTING_AS_ZH)
  })

  it('keeps the end control usable when tenant and expiry are unknown', async () => {
    const onEnd = vi.fn(async () => {})
    // `null` is what an unread optional field looks like at the wire
    // boundary; the cast passes that shape through the declared type.
    const unread = { subject: SUBJECT, expiresAt: null as unknown as Date }
    renderWithProviders(
      <ImpersonationBanner impersonation={unread} onEnd={onEnd} />,
    )
    const region = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    // Neither line is stated: the labels are looked for as fixed text,
    // not as a value the test never passed.
    expect(region).not.toHaveTextContent(TENANT_LABEL_ZH)
    expect(region).not.toHaveTextContent(EXPIRY_LABEL_ZH)
    expect(region).toHaveTextContent(ACTING_AS_ZH)

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.impersonation.end }))
    await waitFor(() => expect(onEnd).toHaveBeenCalledTimes(1))
  })

  it('renders the banner for a malformed expiry instead of throwing out of Intl', () => {
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT, expiresAt: new Date('nope') }}
        onEnd={async () => {}}
      />,
    )
    const region = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    expect(region).toHaveTextContent(ACTING_AS_ZH)
    expect(region).not.toHaveTextContent(EXPIRY_LABEL_ZH)
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    ).toBeEnabled()
  })

  it('disables and marks the end control busy while the attempt is in flight', async () => {
    let resolveEnd: () => void = () => {}
    const onEnd = (): Promise<void> =>
      new Promise((resolve) => {
        resolveEnd = resolve
      })
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={onEnd}
      />,
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.impersonation.end }))

    const busy = await screen.findByRole('button', {
      name: zhCN.impersonation.ending,
    })
    expect(busy).toBeDisabled()

    await act(async () => {
      resolveEnd()
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.impersonation.end }),
      ).toBeEnabled(),
    )
  })

  it('confirms a resolved end in the live region, and the text outlives the banner', async () => {
    // The host's own shape: it clears the impersonation once the end
    // settles, and the banner therefore unmounts while the component --
    // and its one live region -- stays mounted.
    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <ImpersonationBanner
          impersonation={impersonation}
          onEnd={async () => {
            setImpersonation(null)
          }}
        />
      )
    }
    renderWithProviders(<Host />)
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.impersonation.end }))

    await waitFor(() =>
      expect(screen.queryByRole('region')).not.toBeInTheDocument(),
    )
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(zhCN.impersonation.ended)
    // The confirmed text is shown, not left inside the 1x1 hidden box.
    expect(isVisuallyHidden(status)).toBe(false)
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
        onEnd={async () => {}}
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
        onEnd={async () => {}}
      />,
    )
    // The region is the announcement's carrier, but the banner itself
    // states the same line: the region must stay visually hidden.
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(ACTING_AS_ZH)
    expect(isVisuallyHidden(status)).toBe(true)
  })

  it('confirms the end while the banner is still mounted, and the confirmation outlives an explicit clear', async () => {
    // A real host's end write settles over a network round trip: the
    // promise resolves before the host's own re-read clears
    // `impersonation`. The confirmation must be readable in the region
    // while the banner is still on screen, and must stay after the
    // host clears it.
    function Host() {
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
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    )
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
    expect(
      screen.getByRole('region', { name: zhCN.impersonation.regionLabel }),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'clear' }))
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.impersonation.ended,
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
            onEnd={async () => {}}
          />
        </>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Host />)
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    )
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.impersonation.ended,
      ),
    )
    await user.click(screen.getByRole('button', { name: 'clear' }))
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.impersonation.ended,
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

  it('renders a failed end inside the banner with the answer code text, and stays retryable', async () => {
    const onEnd = vi.fn(async () => {
      throw apiError(503, 'authn.unavailable')
    })
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={onEnd}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    )

    const region = screen.getByRole('region', {
      name: zhCN.impersonation.regionLabel,
    })
    const alert = await within(region).findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
    // The control is back and a second click is a new attempt.
    expect(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    ).toBeEnabled()
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonation.end }),
    )
    await waitFor(() => expect(onEnd).toHaveBeenCalledTimes(2))
  })

  it('surfaces a bare 404 from onEnd as that code text, never as an invented "already ended" result', async () => {
    // "There is no impersonation to end" is the host's own concern: its
    // onEnd treats the 404 as settled. The component inspects no HTTP
    // status, so a rejection that reaches it is a failure like any other
    // and renders the bare-status leaf from the package's whitelist.
    const onEnd = vi.fn(async () => {
      throw apiError(404, 'client.http.404')
    })
    renderWithProviders(
      <ImpersonationBanner
        impersonation={{ subject: SUBJECT }}
        onEnd={onEnd}
      />,
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.impersonation.end }))

    const expected = zhCN.errors.client.http.replace('{{status}}', '404')
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(expected),
    )
    expect(onEnd).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(zhCN.impersonation.ended)).not.toBeInTheDocument()
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
          onEnd={async () => {}}
        />
      </div>,
    )
    await expectNoAxeViolations()
  })
})
