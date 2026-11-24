import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { chipClasses } from '@mui/material/Chip'
import {
  ACCOUNT_UI_NAMESPACE,
  accountUiResources,
  SessionsSection,
} from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  ADMIN_UI_NAMESPACE,
  adminUiResources,
  SessionsPage,
} from '@speed/admin-ui'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'

const accountEnUS = accountUiResources['en-US'] as {
  readonly sessions: { readonly current: string }
}
const adminEnUS = adminUiResources['en-US'] as {
  readonly sessions: {
    readonly current: string
    readonly accountLabel: string
  }
}

const ACCOUNT_SESSION = {
  id: 'session-1',
  kind: 'browser',
  created_at: '2026-10-15T12:00:00Z',
  last_seen_at: '2026-10-15T12:30:00Z',
  expires_at: '2026-10-16T12:00:00Z',
  client_ip: '127.0.0.1',
  user_agent: 'test',
  current: true,
}

function renderUnderProviders(ui: ReactNode) {
  const i18n = createI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
  })
  registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
  registerNamespace(i18n, ACCOUNT_UI_NAMESPACE, accountUiResources)
  registerNamespace(i18n, ADMIN_UI_NAMESPACE, adminUiResources)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
}

const CHIP_COLOR_CLASSES = [
  chipClasses.colorPrimary,
  chipClasses.colorSecondary,
  chipClasses.colorSuccess,
  chipClasses.colorError,
  chipClasses.colorInfo,
  chipClasses.colorWarning,
  chipClasses.colorDefault,
]

function chipColorClass(label: string): string {
  const text = screen.getByText(label)
  const chip = text.closest<HTMLElement>(`.${chipClasses.root}`)
  if (chip === null) {
    throw new Error(`no chip carries "${label}"`)
  }
  const color = Array.from(chip.classList).find((name) =>
    CHIP_COLOR_CLASSES.includes(name),
  )
  if (color === undefined) {
    throw new Error(`the chip for "${label}" carries no color class`)
  }
  return color
}

afterEach(() => {
  bindRequestFn((path) => {
    throw new Error(`unexpected request: ${path}`)
  })
})

describe('the current-session chip', () => {
  it('uses one color on the account page and the admin', async () => {
    bindRequestFn(async <T,>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/sessions') {
        return { sessions: [ACCOUNT_SESSION] } as T
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const accountView = renderUnderProviders(<SessionsSection />)
    await screen.findByText(accountEnUS.sessions.current)
    const accountColor = chipColorClass(accountEnUS.sessions.current)
    expect(accountColor).toBe(chipClasses.colorPrimary)
    accountView.unmount()

    bindRequestFn(async <T,>(path: string): Promise<T> => {
      if (path === '/api/v1/system/auth/accounts') {
        return {
          accounts: [
            {
              id: 'acc-1',
              kind: 'user',
              status: 'active',
              display_name: 'Ada',
              mfa_required: false,
              protected: false,
              password_set: true,
              attributes: {},
              identifiers: [],
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
          next_cursor: '',
        } as T
      }
      if (path === '/api/v1/system/auth/accounts/acc-1/sessions') {
        return {
          sessions: [
            {
              id: 'session-1',
              kind: 'refresh',
              assurance: 2,
              auth_time: '2026-10-15T12:00:00Z',
              created_at: '2026-10-15T12:00:00Z',
              last_seen_at: '2026-10-15T12:30:00Z',
              expires_at: '2026-10-16T12:00:00Z',
              revoked: false,
              current: true,
            },
          ],
        } as T
      }
      throw new Error(`unexpected request: ${path}`)
    })
    renderUnderProviders(<SessionsPage />)
    const user = userEvent.setup()
    const picker = screen.getByRole('combobox', {
      name: adminEnUS.sessions.accountLabel,
    })
    await user.click(picker)
    await user.type(picker, 'Ada')
    await user.click(await screen.findByRole('option', { name: 'Ada' }))
    await waitFor(() => expect(picker).toHaveValue('Ada'))

    await screen.findByText(adminEnUS.sessions.current)
    expect(chipColorClass(adminEnUS.sessions.current)).toBe(accountColor)
  })
})
