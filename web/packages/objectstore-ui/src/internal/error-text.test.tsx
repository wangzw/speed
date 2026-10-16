import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { switchLanguage } from '@speed/i18n'
import { renderWithProviders } from '../../test-utils/render.js'
import { useObjectstoreUiErrorText } from './error-text.js'

/** A probe that renders the resolver's text for each code, one per line. */
function Probe({ codes }: { readonly codes: readonly string[] }) {
  const resolve = useObjectstoreUiErrorText()
  return (
    <ul>
      {codes.map((code) => (
        <li key={code} data-code={code}>
          {resolve(code)}
        </li>
      ))}
    </ul>
  )
}

function textFor(code: string): string {
  const item = screen.getByText((_content, element) => {
    return element?.getAttribute('data-code') === code
  })
  return item.textContent ?? ''
}

describe('useObjectstoreUiErrorText', () => {
  it('resolves a transport code to its own text', () => {
    renderWithProviders(
      <Probe codes={['client.network', 'client.timeout', 'client.protocol']} />,
      { language: 'en-US' },
    )
    expect(textFor('client.network')).toMatch(/network connection/i)
    expect(textFor('client.timeout')).toMatch(/timed out/i)
    expect(textFor('client.protocol')).toMatch(/unexpected response/i)
  })

  it('resolves the client.http.<status> family through its parameterized leaf', () => {
    renderWithProviders(
      <Probe codes={['client.http.409', 'client.http.503']} />,
      { language: 'en-US' },
    )
    expect(textFor('client.http.409')).toContain('409')
    expect(textFor('client.http.503')).toContain('503')
  })

  it('falls back to the unknown text for an unlisted code', () => {
    // The objectstore refusals arrive as client.http.<status> (their
    // problem body carries no code), so a module code is not whitelisted
    // and must render the generic fallback, never a raw key.
    renderWithProviders(<Probe codes={['objectstore.conflict']} />, {
      language: 'en-US',
    })
    expect(textFor('objectstore.conflict')).toMatch(/went wrong/i)
  })

  it('re-renders the resolved text when the language switches', async () => {
    const { i18n } = renderWithProviders(<Probe codes={['client.network']} />, {
      language: 'en-US',
    })
    expect(textFor('client.network')).toMatch(/network connection/i)
    await switchLanguage(i18n, 'zh-CN')
    await waitFor(() => {
      expect(textFor('client.network')).toContain('网络连接')
    })
  })
})
