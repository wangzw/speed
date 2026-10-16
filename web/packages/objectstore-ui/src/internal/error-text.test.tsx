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

  it('resolves the objectstore module refusals to their own text', () => {
    // The module's refusals arrive as the code derived from their problem
    // type urn:speed:objectstore:<code>, i.e. objectstore.not_found and
    // friends, so each resolves to its own leaf, never the generic
    // fallback.
    renderWithProviders(
      <Probe
        codes={[
          'objectstore.bad_request',
          'objectstore.not_found',
          'objectstore.conflict',
        ]}
      />,
      { language: 'en-US' },
    )
    expect(textFor('objectstore.bad_request')).toMatch(/could not be read/i)
    expect(textFor('objectstore.not_found')).toMatch(/no longer available/i)
    expect(textFor('objectstore.conflict')).toMatch(/state does not allow/i)
  })

  it('resolves the authorization guard permission refusal', () => {
    renderWithProviders(<Probe codes={['authz.forbidden']} />, {
      language: 'en-US',
    })
    expect(textFor('authz.forbidden')).toMatch(/permission/i)
  })

  it('falls back to the unknown text for an unlisted code', () => {
    // A code outside the whitelist renders the shared fallback, never a
    // raw key.
    renderWithProviders(<Probe codes={['objectstore.future_code']} />, {
      language: 'en-US',
    })
    expect(textFor('objectstore.future_code')).toMatch(/went wrong/i)
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
