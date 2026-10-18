import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { renderWithProviders } from '../test-utils/render.js'
import {
  jsonResponse,
  makeObjectStoreRig,
  problemResponse,
} from '../test-utils/real-client.js'
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

  it('renders the tenant state on the admission layer bare 404, without a retry', async () => {
    makeObjectStoreRig(() => new Response(null, { status: 404 }))
    renderWithProviders(<ObjectDetailDialog id="o-1" onClose={() => undefined} />, {
      language: 'en-US',
    })
    const dialog = await screen.findByRole('dialog')
    expect(
      await within(dialog).findByText(
        "Cannot access the current organization's content",
      ),
    ).toBeInTheDocument()
    expect(
      within(dialog).queryByRole('button', { name: 'Try again' }),
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByText('The object could not be loaded'),
    ).not.toBeInTheDocument()
  })

  it('names a typed objectstore not-found 404 as gone, without a retry', async () => {
    makeObjectStoreRig(() =>
      problemResponse(404, 'urn:speed:objectstore:not-found'),
    )
    renderWithProviders(<ObjectDetailDialog id="o-1" onClose={() => undefined} />, {
      language: 'en-US',
    })
    const dialog = await screen.findByRole('dialog')
    expect(
      await within(dialog).findByText('This object is no longer available'),
    ).toBeInTheDocument()
    expect(
      within(dialog).getByText(
        /may have been deleted or have expired/i,
      ),
    ).toBeInTheDocument()
    // The server answered and said the object is gone: a retry cannot
    // bring it back, and the failure is not the store being unreachable.
    expect(
      within(dialog).queryByRole('button', { name: 'Try again' }),
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByText('The object could not be loaded'),
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByText(/did not answer/i),
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByText("Cannot access the current organization's content"),
    ).not.toBeInTheDocument()
  })

  it('keeps the retryable detail error when the read fails with a 5xx', async () => {
    makeObjectStoreRig(() =>
      problemResponse(503, 'urn:speed:objectstore:unavailable'),
    )
    renderWithProviders(<ObjectDetailDialog id="o-1" onClose={() => undefined} />, {
      language: 'en-US',
    })
    const dialog = await screen.findByRole('dialog')
    expect(
      await within(dialog).findByText('The object could not be loaded'),
    ).toBeInTheDocument()
    expect(
      within(dialog).getByText(
        /could not complete this request/i,
      ),
    ).toBeInTheDocument()
    expect(
      within(dialog).getByRole('button', { name: 'Try again' }),
    ).toBeInTheDocument()
    expect(
      within(dialog).queryByText('This object is no longer available'),
    ).not.toBeInTheDocument()
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
