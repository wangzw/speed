/**
 * Shared DOM-test harness for objectstore-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider around AppThemeProvider around QueryClientProvider
 * around auth-core's TenantProvider. The page reads its data through the
 * react-query hooks generated into @speed/api-sdk, so it needs a query
 * client; it also reads the current tenant from the tenant context, so it
 * needs a TenantProvider (the tenant namespaces every query key and gates
 * the reads). The client is fresh per call and retries nothing, so an
 * operation the test scripts to fail surfaces on the first attempt and a
 * mutation never outlives its test.
 *
 * The i18n instance is created per call through the shared createTestI18n
 * (see @speed/test-utils/render's header), with both namespaces a rendered
 * component reads registered: the objectstore-ui namespace for this
 * package's own strings and the ui-kit namespace, because the page composes
 * ui-kit components whose built-in strings speak ui-kit-namespace keys.
 *
 * The harness renders no page heading of its own: ObjectsPage renders its
 * own h1 through ui-kit's PageHeader, so a scan over it is honest without
 * extra context. A test that renders a heading-less fragment (a dialog on
 * its own) supplies the page heading in that test.
 *
 * Tests that exercise a language switch keep the returned instance and act
 * on it (await switchLanguage(i18n, 'en-US')); the provider's
 * 'languageChanged' subscription re-renders the tree.
 */

import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'
import { I18nextProvider, registerNamespace } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { TenantProvider } from '@speed/auth-core'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import {
  createTestI18n,
  createTestQueryClient,
} from '@speed/test-utils/render'
import type { RenderWithProvidersOptions } from '@speed/test-utils/render'
import type { RenderWithProvidersResult as BaseResult } from '@speed/test-utils/render'
import {
  OBJECTSTORE_UI_NAMESPACE,
  objectstoreUiResources,
} from '../src/resources.js'

export type { RenderWithProvidersOptions }

export interface RenderWithProvidersResult extends BaseResult {
  /** The query client the tree renders with; tests that seed or inspect
   * react-query state act on it. */
  readonly queryClient: QueryClient
}

/** Fresh bilingual instance with both namespaces registered. */
export function createObjectstoreUiI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, OBJECTSTORE_UI_NAMESPACE, objectstoreUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export { createTestQueryClient }

export interface ObjectstoreRenderOptions extends RenderWithProvidersOptions {
  /** The current tenant the tree renders under; defaults to 'acme'. Pass
   * null to render with no tenant (the reads stay disabled). */
  readonly tenantId?: string | null
}

export function renderWithProviders(
  ui: ReactElement,
  options: ObjectstoreRenderOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n, tenantId = 'acme' } = options
  const instance = i18n ?? createObjectstoreUiI18n(language)
  const queryClient = createTestQueryClient()
  const result = render(
    <I18nextProvider i18n={instance}>
      <AppThemeProvider i18n={instance}>
        <QueryClientProvider client={queryClient}>
          <TenantProvider tenantId={tenantId}>{ui}</TenantProvider>
        </QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
  return { ...result, i18n: instance, queryClient }
}
