import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import { RefreshStatus } from './refresh-status.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }

const ZH = zhCN.common as {
  readonly updatedAt: string
  readonly refreshed: string
}
const EN = enUS.common as {
  readonly updatedAt: string
  readonly refreshed: string
}

const SETTLED_AT = Date.UTC(2026, 9, 15, 12, 0, 0)

function longTime(language: string): string {
  return new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'long',
    timeZone: TEST_TIME_ZONE,
  }).format(new Date(SETTLED_AT))
}

describe('RefreshStatus', () => {
  it('renders no update time before a read has settled and keeps the region empty', () => {
    renderWithProviders(<RefreshStatus updatedAt={undefined} announcements={0} />)
    const prefix = ZH.updatedAt.split('{{time}}')[0] ?? ZH.updatedAt
    expect(
      screen.queryByText((content) => content.includes(prefix)),
    ).toBeNull()
    expect(screen.getByRole('status').textContent).toBe('')
  })

  it.each([
    { language: 'zh-CN', copy: ZH },
    { language: 'en-US', copy: EN },
  ])(
    "formats the update time in $language with seconds in the harness's default zone",
    ({ language, copy }) => {
      renderWithProviders(
        <RefreshStatus updatedAt={SETTLED_AT} announcements={0} />,
        { language },
      )
      expect(
        screen.getByText(copy.updatedAt.replace('{{time}}', longTime(language))),
      ).toBeInTheDocument()
    },
  )

  it('fills the completion region once a refresh has completed', () => {
    renderWithProviders(
      <RefreshStatus updatedAt={SETTLED_AT} announcements={1} />,
    )
    expect(screen.getByRole('status').textContent).toBe(ZH.refreshed)
  })
})
