/**
 * The render-primitive layer shared by the packages' and the app's DOM
 * harnesses: TEST_LANGUAGES, the deterministic createTestI18n base, the
 * retries-nothing createTestQueryClient, the RenderWithProviders option and
 * result shapes, and TestTimeZoneProvider.
 */

import type { ReactNode } from 'react'
import type { RenderResult } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { createI18n } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'

export const TEST_TIME_ZONE = 'America/New_York'

export interface RenderWithProvidersOptions {
  readonly language?: string
  readonly i18n?: I18nInstance
  readonly timeZone?: string
}

export interface TestTimeZoneProviderProps {
  readonly timeZone?: string
  readonly children?: ReactNode
}

export function TestTimeZoneProvider({
  timeZone = TEST_TIME_ZONE,
  children,
}: TestTimeZoneProviderProps) {
  return (
    <DisplayTimeZoneProvider timeZone={timeZone}>
      {children}
    </DisplayTimeZoneProvider>
  )
}

export interface RenderWithProvidersResult extends RenderResult {
  readonly i18n: I18nInstance
}

export const TEST_LANGUAGES = ['zh-CN', 'en-US'] as const

// Each call must return a fresh instance: a second namespace registration on
// one instance throws.
export function createTestI18n(language: string = 'zh-CN'): I18nInstance {
  return createI18n({
    supportedLanguages: TEST_LANGUAGES,
    defaultLanguage: language,
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
  })
}

export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 0,
        refetchOnMount: true,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
      },
      mutations: { retry: false },
    },
  })
}
