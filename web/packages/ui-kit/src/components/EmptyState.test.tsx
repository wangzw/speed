
import { act } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useTranslation, switchLanguage } from '@speed/i18n'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import {
  expectNoAxeViolations,
  runHeadingOrderCheck,
} from '../../test-utils/axe.js'
import { EmptyState } from './EmptyState.js'

describe('EmptyState', () => {
  it('renders the empty variant with its built-in zh-CN texts by default', () => {
    const { getByText } = renderWithProviders(<EmptyState />)
    expect(getByText(zhCN.emptyState.empty.title)).toBeInTheDocument()
    expect(getByText(zhCN.emptyState.empty.description)).toBeInTheDocument()
  })

  it.each(['empty', 'noPermission', 'error'] as const)(
    'renders the %s variant with its own built-in texts',
    (variant) => {
      const { getByText } = renderWithProviders(<EmptyState variant={variant} />)
      expect(getByText(zhCN.emptyState[variant].title)).toBeInTheDocument()
      expect(getByText(zhCN.emptyState[variant].description)).toBeInTheDocument()
    },
  )

  it('switches built-in texts to the en-US bundle when the language changes', async () => {
    const { i18n, getByText, queryByText } = renderWithProviders(<EmptyState />)
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(getByText(enUS.emptyState.empty.title)).toBeInTheDocument()
    expect(getByText(enUS.emptyState.empty.description)).toBeInTheDocument()
    expect(queryByText(zhCN.emptyState.empty.title)).not.toBeInTheDocument()
  })

  it('lets title and description overrides replace the built-in texts', () => {
    const { getByText, queryByText } = renderWithProviders(
      <EmptyState title="Tenant at capacity" description="Upgrade to store more scans." />,
    )
    expect(getByText('Tenant at capacity')).toBeInTheDocument()
    expect(getByText('Upgrade to store more scans.')).toBeInTheDocument()
    expect(queryByText(zhCN.emptyState.empty.title)).not.toBeInTheDocument()
    expect(queryByText(zhCN.emptyState.empty.description)).not.toBeInTheDocument()
  })

  it('renders an action below the description when given', () => {
    const { getByRole } = renderWithProviders(
      <EmptyState action={<button type="button">Create first scan</button>} />,
    )
    expect(getByRole('button', { name: 'Create first scan' })).toBeInTheDocument()
  })

  it('renders the custom icon in place of the variant stock icon', () => {
    const { getByTestId, container } = renderWithProviders(
      <EmptyState icon={<div data-testid="custom-icon" />} />,
    )
    expect(getByTestId('custom-icon')).toBeInTheDocument()
    expect(container.querySelectorAll('svg')).toHaveLength(0)
  })

  it('keeps the stock icons decorative (aria-hidden, no focus)', () => {
    const { container } = renderWithProviders(<EmptyState variant="noPermission" />)
    for (const svg of container.querySelectorAll('svg')) {
      expect(svg).toHaveAttribute('aria-hidden', 'true')
      expect(svg).toHaveAttribute('focusable', 'false')
    }
  })

  it('renders a missing namespace key as the key itself, warning visibly', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    function MissingProbe() {
      const { t } = useTranslation('ui-kit')
      return <div data-testid="probe">{t('emptyState.empty.title.typo')}</div>
    }
    const { getByTestId } = renderWithProviders(<MissingProbe />)
    try {
      expect(getByTestId('probe')).toHaveTextContent('emptyState.empty.title.typo')
      expect(warn).toHaveBeenCalledTimes(1)
      const message = warn.mock.calls[0]?.[0] as string
      expect(message).toContain('emptyState.empty.title.typo')
      expect(message).toContain('"ui-kit"')
    } finally {
      warn.mockRestore()
    }
  })

  it('passes axe over every variant', async () => {
    for (const variant of ['empty', 'noPermission', 'error'] as const) {
      renderWithProviders(
        <div>
          <h1>Page title</h1>
          <EmptyState variant={variant} headingLevel="h2" />
        </div>,
      )
    }
    await expectNoAxeViolations()
  })

  it('fails the axe scan when the document has no page heading instead of passing by indeterminacy', async () => {
    renderWithProviders(<EmptyState headingLevel="h2" />)
    await expect(expectNoAxeViolations()).rejects.toThrow(/page-has-heading-one/)
  })

  it('does not skip a heading level under a real h1/h2 ancestor', async () => {
    renderWithProviders(
      <div>
        <h1>Page title</h1>
        <EmptyState />
      </div>,
    )
    const heading = document.querySelector('h6')
    expect(heading).not.toBeNull()

    const violations = await runHeadingOrderCheck()
    expect(violations.length).toBeGreaterThan(0)
  })

  it('continues the real page heading order when headingLevel is supplied', async () => {
    renderWithProviders(
      <div>
        <h1>Page title</h1>
        <EmptyState headingLevel="h2" />
      </div>,
    )
    expect(document.querySelector('h6')).toBeNull()
    expect(document.querySelector('h2')).not.toBeNull()

    const violations = await runHeadingOrderCheck()
    expect(violations).toHaveLength(0)
  })

  it('renders the title headerless in a status region when headingLevel is none', () => {
    const { getByText, container } = renderWithProviders(
      <div>
        <h1>Page title</h1>
        <EmptyState headingLevel="none" />
      </div>,
    )
    expect(container.querySelector('h6')).toBeNull()
    expect(container.querySelectorAll('h1, h2, h3, h4, h5, h6')).toHaveLength(1)
    const status = container.querySelector('[role="status"]')
    expect(status).not.toBeNull()
    expect(status).toHaveTextContent(zhCN.emptyState.empty.title)
    expect(getByText(zhCN.emptyState.empty.title)).toBeInTheDocument()
  })
})
