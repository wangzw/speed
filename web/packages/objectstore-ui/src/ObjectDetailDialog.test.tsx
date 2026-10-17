import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { renderWithProviders } from '../test-utils/render.js'
import { jsonResponse, makeObjectStoreRig } from '../test-utils/real-client.js'
import { ObjectDetailDialog } from './ObjectDetailDialog.js'

const OBJECT = {
  id: 'o-1',
  name: 'report.txt',
  state: 'completed',
  size: 3,
  mime: 'text/plain',
  checksum_sha256: 'deadbeef',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

describe('ObjectDetailDialog', () => {
  it('renders the object fields and the "never" copy for an absent expiry', async () => {
    makeObjectStoreRig(() => jsonResponse(200, { object: OBJECT }))
    renderWithProviders(<ObjectDetailDialog id="o-1" onClose={() => undefined} />, {
      language: 'en-US',
    })
    const dialog = await screen.findByRole('dialog')
    await waitFor(() => {
      expect(within(dialog).getByText('deadbeef')).toBeInTheDocument()
    })
    expect(within(dialog).getByText('Object ID')).toBeInTheDocument()
    // The full name lives here, where the list's truncated cell points.
    expect(within(dialog).getByText('Name')).toBeInTheDocument()
    expect(within(dialog).getByText('report.txt')).toBeInTheDocument()
    expect(within(dialog).getByText('Never')).toBeInTheDocument()
  })

  it('renders the unnamed copy for an object with no name', async () => {
    makeObjectStoreRig(() =>
      jsonResponse(200, { object: { ...OBJECT, name: '' } }),
    )
    renderWithProviders(<ObjectDetailDialog id="o-1" onClose={() => undefined} />, {
      language: 'en-US',
    })
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('Unnamed')).toBeInTheDocument()
  })

  it('reads nothing while closed (id null)', async () => {
    const rig = makeObjectStoreRig(() => {
      throw new Error('no request should be sent while the dialog is closed')
    })
    renderWithProviders(<ObjectDetailDialog id={null} onClose={() => undefined} />, {
      language: 'en-US',
    })
    await Promise.resolve()
    expect(rig.calls).toHaveLength(0)
  })
})
