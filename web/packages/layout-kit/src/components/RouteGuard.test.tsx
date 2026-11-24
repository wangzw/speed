import { act } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { switchLanguage } from '@speed/i18n'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { RouteGuard } from './RouteGuard.js'

describe('RouteGuard', () => {
  describe('allowed', () => {
    it('renders children and nothing else', () => {
      const { getByText, queryByText } = renderWithProviders(
        <RouteGuard status="allowed">
          <p>Protected content</p>
        </RouteGuard>,
      )
      expect(getByText('Protected content')).toBeInTheDocument()
      expect(queryByText(uiKitZhCN.emptyState.noPermission.title)).not.toBeInTheDocument()
    })

    it('has no axe violations', async () => {
      renderWithProviders(
        <main>
          <h1>Protected page</h1>
          <RouteGuard status="allowed">
            <p>Protected content</p>
          </RouteGuard>
        </main>,
      )
      await expectNoAxeViolations({ disabledRules: ['region'] })
    })
  })

  describe('pending', () => {
    it('renders the default labelled spinner and no children', () => {
      const { getByRole, queryByText } = renderWithProviders(
        <RouteGuard status="pending">
          <p>Protected content</p>
        </RouteGuard>,
      )
      expect(getByRole('progressbar', { name: zhCN.routeGuard.pending })).toBeInTheDocument()
      expect(queryByText('Protected content')).not.toBeInTheDocument()
    })

    it('relabels the default spinner when the language switches to en-US', async () => {
      const { getByRole, i18n } = renderWithProviders(<RouteGuard status="pending" />)
      await act(async () => {
        await switchLanguage(i18n, 'en-US')
      })
      expect(getByRole('progressbar', { name: enUS.routeGuard.pending })).toBeInTheDocument()
    })

    it('renders a host-supplied pendingFallback instead of the default', () => {
      const { getByText, queryByRole } = renderWithProviders(
        <RouteGuard status="pending" pendingFallback={<p>Loading your access…</p>} />,
      )
      expect(getByText('Loading your access…')).toBeInTheDocument()
      expect(queryByRole('progressbar')).not.toBeInTheDocument()
    })

    it('has no axe violations', async () => {
      renderWithProviders(
        <main>
          <h1>Protected page</h1>
          <RouteGuard status="pending" />
        </main>,
      )
      await expectNoAxeViolations({ disabledRules: ['region'] })
    })
  })

  describe('denied', () => {
    it('renders the default ui-kit noPermission EmptyState and no children', () => {
      const { getByText, queryByText } = renderWithProviders(
        <RouteGuard status="denied">
          <p>Protected content</p>
        </RouteGuard>,
      )
      expect(getByText(uiKitZhCN.emptyState.noPermission.title)).toBeInTheDocument()
      expect(getByText(uiKitZhCN.emptyState.noPermission.description)).toBeInTheDocument()
      expect(queryByText('Protected content')).not.toBeInTheDocument()
    })

    it('renders a host-supplied deniedFallback instead of the default', () => {
      const { getByText, queryByText } = renderWithProviders(
        <RouteGuard status="denied" deniedFallback={<p>Ask your admin for access.</p>} />,
      )
      expect(getByText('Ask your admin for access.')).toBeInTheDocument()
      expect(queryByText(uiKitZhCN.emptyState.noPermission.title)).not.toBeInTheDocument()
    })

    it('renders the default denied fallback title as the page-level h1', () => {
      const { getByRole } = renderWithProviders(<RouteGuard status="denied" />)
      expect(
        getByRole('heading', { level: 1, name: uiKitZhCN.emptyState.noPermission.title }),
      ).toBeInTheDocument()
    })

    it('renders the default denied fallback title at the host-forwarded headingLevel', () => {
      const { getByRole } = renderWithProviders(
        <main>
          <h1>Protected page</h1>
          <RouteGuard status="denied" headingLevel="h3" />
        </main>,
      )
      expect(
        getByRole('heading', { level: 3, name: uiKitZhCN.emptyState.noPermission.title }),
      ).toBeInTheDocument()
    })

    it('fires onDenied exactly once for a single transition into denied, not on every re-render', () => {
      const onDenied = vi.fn()
      const { rerender } = renderWithProviders(
        <RouteGuard status="pending" onDenied={onDenied} />,
      )
      expect(onDenied).not.toHaveBeenCalled()

      rerender(<RouteGuard status="denied" onDenied={onDenied} />)
      expect(onDenied).toHaveBeenCalledTimes(1)

      rerender(<RouteGuard status="denied" onDenied={onDenied} />)
      expect(onDenied).toHaveBeenCalledTimes(1)
    })

    it('fires onDenied again on a later, separate transition into denied', () => {
      const onDenied = vi.fn()
      const { rerender } = renderWithProviders(
        <RouteGuard status="denied" onDenied={onDenied} />,
      )
      expect(onDenied).toHaveBeenCalledTimes(1)

      rerender(<RouteGuard status="allowed" onDenied={onDenied} />)
      expect(onDenied).toHaveBeenCalledTimes(1)

      rerender(<RouteGuard status="denied" onDenied={onDenied} />)
      expect(onDenied).toHaveBeenCalledTimes(2)
    })

    it('has no axe violations', async () => {
      renderWithProviders(
        <main>
          <RouteGuard status="denied" />
        </main>,
      )
      await expectNoAxeViolations({ disabledRules: ['region'] })
    })

    it('has no axe violations when the host forwards headingLevel under its own page heading', async () => {
      renderWithProviders(
        <main>
          <h1>Protected page</h1>
          <RouteGuard status="denied" headingLevel="h2" />
        </main>,
      )
      await expectNoAxeViolations({ disabledRules: ['region'] })
    })
  })
})
