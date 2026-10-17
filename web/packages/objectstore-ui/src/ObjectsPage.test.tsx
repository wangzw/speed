/**
 * ObjectsPage behaviour over the objectstore routes.
 *
 * What this suite pins, per story: the tenant's objects render from
 * GET /api/v1/objects (identifier, state, size, content type); an empty
 * answer renders the empty state; a failed load renders the retryable error
 * and a 403 renders the no-permission state; the cursor controls page
 * forward and back; a picked file runs the module's three-step transfer --
 * declare (POST /objects) -> upload the bytes (PUT /objects/{id}/content)
 * -> complete (POST /objects/{id}/complete) -- with the file's bytes
 * travelling as a Blob (never JSON text) and the created declaration
 * carrying the file's size and content type, and the list re-reads the
 * committed row; download reads GET /objects/{id}/content as a Blob and
 * hands it to the browser with a MIME-derived filename; delete asks first
 * and only then issues DELETE /objects/{id}; and the details dialog reads
 * GET /objects/{id}. Every request runs through a real @speed/api-client
 * over a scripted fetch stand-in, so the transport's JSON and binary paths
 * are exercised for real, not scripted around.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  blobResponse,
  jsonResponse,
  makeObjectStoreRig,
  noContentResponse,
  problemResponse,
  type RecordedRequest,
} from '../test-utils/real-client.js'
import { ObjectsPage } from './ObjectsPage.js'

// The download side effect is stubbed: the suite asserts the bytes and
// filename handed to it, without jsdom attempting a navigation.
vi.mock('./internal/download.js', () => ({ saveBlob: vi.fn() }))
import { saveBlob } from './internal/download.js'

const OBJECT_A = {
  id: 'o-aaa',
  state: 'completed',
  size: 12,
  mime: 'text/plain',
  checksum_sha256: 'aaaa',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-02T00:00:00Z',
}

const OBJECT_B = {
  ...OBJECT_A,
  id: 'o-bbb',
  state: 'uploaded',
}

const LIST_PATH = '/api/v1/objects'

/** The recorded calls as "METHOD path" strings, for order assertions. */
function sequence(calls: readonly RecordedRequest[]): string[] {
  return calls.map((call) => `${call.method} ${call.path}`)
}

describe('ObjectsPage', () => {
  it("lists the tenant's objects from the list route", async () => {
    makeObjectStoreRig(() => jsonResponse(200, { objects: [OBJECT_A], next_cursor: '' }))
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })

    expect(await screen.findByText('o-aaa')).toBeInTheDocument()
    expect(screen.getByText('Completed')).toBeInTheDocument()
    expect(screen.getByText('12 B')).toBeInTheDocument()
    expect(screen.getByText('text/plain')).toBeInTheDocument()

    await expectNoAxeViolations()
  })

  it('renders the empty state when the tenant has no objects', async () => {
    makeObjectStoreRig(() => jsonResponse(200, { objects: [], next_cursor: '' }))
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    expect(await screen.findByText('No objects yet')).toBeInTheDocument()
  })

  it('renders the retryable error when the list fails', async () => {
    makeObjectStoreRig(() =>
      problemResponse(503, 'urn:speed:objectstore:unavailable'),
    )
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    expect(
      await screen.findByText('The objects could not be loaded'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })

  it('renders the no-permission state on a 403 refusal', async () => {
    makeObjectStoreRig(() => problemResponse(403, 'urn:speed:objectstore:forbidden'))
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    expect(
      await screen.findByText('No access to the object store'),
    ).toBeInTheDocument()
  })

  it('pages through the cursor', async () => {
    const user = userEvent.setup()
    makeObjectStoreRig((call) => {
      if (call.query.includes('cursor=')) {
        return jsonResponse(200, { objects: [OBJECT_B], next_cursor: '' })
      }
      return jsonResponse(200, { objects: [OBJECT_A], next_cursor: 'CURSOR-1' })
    })
    renderWithProviders(<ObjectsPage pageSize={1} />, { language: 'en-US' })

    expect(await screen.findByText('o-aaa')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Next page' }))
    expect(await screen.findByText('o-bbb')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(await screen.findByText('o-aaa')).toBeInTheDocument()
  })

  it('does not read when no tenant is in play', async () => {
    const rig = makeObjectStoreRig(() => {
      throw new Error('no request should be sent without a tenant')
    })
    renderWithProviders(<ObjectsPage />, { language: 'en-US', tenantId: null })

    // The read is disabled, so the loading announcement stands and no
    // request was made.
    expect(
      screen.getByRole('status', { name: 'Loading objects' }),
    ).toBeInTheDocument()
    await Promise.resolve()
    expect(rig.calls).toHaveLength(0)
  })

  it('uploads a file through declare -> upload -> complete', async () => {
    const user = userEvent.setup()
    let completed = false
    const stored = { ...OBJECT_A, id: 'o-new', mime: 'text/plain', size: 5 }
    const rig = makeObjectStoreRig((call) => {
      if (call.method === 'GET' && call.path === LIST_PATH) {
        return jsonResponse(200, {
          objects: completed ? [stored] : [],
          next_cursor: '',
        })
      }
      if (call.method === 'POST' && call.path === LIST_PATH) {
        return jsonResponse(201, { object: { ...stored, state: 'pending' } })
      }
      if (call.method === 'PUT' && call.path === `${LIST_PATH}/o-new/content`) {
        return noContentResponse()
      }
      if (
        call.method === 'POST' &&
        call.path === `${LIST_PATH}/o-new/complete`
      ) {
        completed = true
        return jsonResponse(200, { object: stored })
      }
      throw new Error(`unexpected request ${call.method} ${call.path}`)
    })

    const { container } = renderWithProviders(<ObjectsPage />, {
      language: 'en-US',
    })
    await screen.findByText('No objects yet')

    const input = container.querySelector('input[type="file"]')
    expect(input).not.toBeNull()
    const file = new File(['hello'], 'greeting.txt', { type: 'text/plain' })
    await user.upload(input as HTMLInputElement, file)

    // The queue shows the picked file and settles as uploaded.
    expect(await screen.findByText('greeting.txt')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.getByText('Uploaded')).toBeInTheDocument()
    })

    // The three steps ran in order.
    const order = sequence(rig.calls).filter((entry) => entry !== `GET ${LIST_PATH}`)
    expect(order).toEqual([
      `POST ${LIST_PATH}`,
      `PUT ${LIST_PATH}/o-new/content`,
      `POST ${LIST_PATH}/o-new/complete`,
    ])

    // The declaration carried the file's size and content type.
    const create = rig.calls.find(
      (call) => call.method === 'POST' && call.path === LIST_PATH,
    )
    expect(JSON.parse(create?.body as string)).toEqual({
      name: 'greeting.txt',
      size: 5,
      content_type: 'text/plain',
    })

    // The byte leg carried the file's bytes as a Blob, not JSON text.
    const put = rig.calls.find((call) => call.method === 'PUT')
    expect(put?.body).toBeInstanceOf(Blob)
    expect((put?.body as Blob).size).toBe(5)
    expect(await (put?.body as Blob).text()).toBe('hello')

    // The list re-read the committed row.
    expect(await screen.findByText('o-new')).toBeInTheDocument()
  })

  it("downloads an object's bytes with a MIME-derived filename", async () => {
    const user = userEvent.setup()
    vi.mocked(saveBlob).mockClear()
    makeObjectStoreRig((call) => {
      if (call.method === 'GET' && call.path === `${LIST_PATH}/o-aaa/content`) {
        return blobResponse('the-bytes', 'text/plain')
      }
      return jsonResponse(200, { objects: [OBJECT_A], next_cursor: '' })
    })
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    await screen.findByText('o-aaa')

    await user.click(screen.getByRole('button', { name: 'Download object o-aaa' }))

    await waitFor(() => expect(saveBlob).toHaveBeenCalledTimes(1))
    const [blob, filename] = vi.mocked(saveBlob).mock.calls[0] as [Blob, string]
    // The bytes the store answered with reached the browser unchanged.
    expect(await blob.text()).toBe('the-bytes')
    expect(filename).toBe('o-aaa.txt')
  })

  it('deletes an object only after the confirmation', async () => {
    const user = userEvent.setup()
    let deleted = false
    const rig = makeObjectStoreRig((call) => {
      if (call.method === 'DELETE' && call.path === `${LIST_PATH}/o-aaa`) {
        deleted = true
        return noContentResponse()
      }
      return jsonResponse(200, {
        objects: deleted ? [] : [OBJECT_A],
        next_cursor: '',
      })
    })
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    await screen.findByText('o-aaa')

    await user.click(screen.getByRole('button', { name: 'Delete object o-aaa' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Delete this object?')).toBeInTheDocument()
    // Nothing has been deleted yet: the control only asked.
    expect(rig.calls.some((call) => call.method === 'DELETE')).toBe(false)

    // Deleting an object discards its bytes for good, so the confirmation
    // carries the heavier two-step guard: the first click only arms.
    const confirmAgain = (
      uiKitResources['en-US'] as unknown as {
        readonly confirmDialog: { readonly confirmAgainLabel: string }
      }
    ).confirmDialog.confirmAgainLabel
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(rig.calls.some((call) => call.method === 'DELETE')).toBe(false)
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(within(dialog).getByRole('button', { name: confirmAgain }))
    await waitFor(() => {
      expect(
        rig.calls.some(
          (call) => call.method === 'DELETE' && call.path === `${LIST_PATH}/o-aaa`,
        ),
      ).toBe(true)
    })
    expect(await screen.findByText('No objects yet')).toBeInTheDocument()
    // The landed delete confirms itself in the page's status region, rather
    // than succeeding silently.
    expect(await screen.findByText('Object deleted')).toBeInTheDocument()
  })

  it('opens the details dialog and reads the single object', async () => {
    const user = userEvent.setup()
    const rig = makeObjectStoreRig((call) => {
      if (call.method === 'GET' && call.path === `${LIST_PATH}/o-aaa`) {
        return jsonResponse(200, { object: OBJECT_A })
      }
      return jsonResponse(200, { objects: [OBJECT_A], next_cursor: '' })
    })
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    await screen.findByText('o-aaa')

    await user.click(screen.getByRole('button', { name: 'Details' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Object details')).toBeInTheDocument()

    await waitFor(() => {
      expect(
        rig.calls.some((call) => call.path === `${LIST_PATH}/o-aaa`),
      ).toBe(true)
    })
    // The dialog's field list carries the object's values.
    expect(within(dialog).getByText('Object ID')).toBeInTheDocument()
    expect(within(dialog).getByText('aaaa')).toBeInTheDocument()
  })

  it('retries a failed upload and removes the settled row', async () => {
    const user = userEvent.setup()
    let createAttempts = 0
    const stored = { ...OBJECT_A, id: 'o-retry', mime: 'text/plain', size: 5 }
    makeObjectStoreRig((call) => {
      if (call.method === 'GET' && call.path === LIST_PATH) {
        return jsonResponse(200, { objects: [], next_cursor: '' })
      }
      if (call.method === 'POST' && call.path === LIST_PATH) {
        createAttempts += 1
        if (createAttempts === 1) {
          return problemResponse(503, 'urn:speed:objectstore:unavailable')
        }
        return jsonResponse(201, { object: { ...stored, state: 'pending' } })
      }
      if (call.method === 'PUT' && call.path === `${LIST_PATH}/o-retry/content`) {
        return noContentResponse()
      }
      if (
        call.method === 'POST' &&
        call.path === `${LIST_PATH}/o-retry/complete`
      ) {
        return jsonResponse(200, { object: stored })
      }
      throw new Error(`unexpected request ${call.method} ${call.path}`)
    })
    const { container } = renderWithProviders(<ObjectsPage />, {
      language: 'en-US',
    })
    await screen.findByText('No objects yet')

    const input = container.querySelector('input[type="file"]')
    await user.upload(input as HTMLInputElement, new File(['hello'], 'retry-me.txt', { type: 'text/plain' }))

    // The first attempt fails; the row offers a retry.
    expect(await screen.findByText('Upload failed')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => {
      expect(screen.getByText('Uploaded')).toBeInTheDocument()
    })

    // The settled row is removable.
    await user.click(screen.getByRole('button', { name: 'Remove' }))
    await waitFor(() => {
      expect(screen.queryByText('retry-me.txt')).not.toBeInTheDocument()
    })
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    makeObjectStoreRig(() =>
      jsonResponse(200, { objects: [OBJECT_A], next_cursor: 'cursor-1' }),
    )
    renderWithProviders(<ObjectsPage />, { language: 'en-US' })
    await screen.findByText('o-aaa')
    // The row actions, the toolbar refresh and the cursor pager are all
    // compact controls; on a finger-driven device each one gates up to
    // the 44x44 floor through @speed/ui-kit's touchTargetSx.
    const gated = [
      screen.getByRole('button', { name: 'Download object o-aaa' }),
      screen.getByRole('button', { name: 'Details' }),
      screen.getByRole('button', { name: 'Delete object o-aaa' }),
      screen.getByRole('button', { name: 'Refresh' }),
      screen.getByRole('button', { name: 'Previous page' }),
      screen.getByRole('button', { name: 'Next page' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('renders in Chinese when the active language is zh-CN', async () => {
    makeObjectStoreRig(() => jsonResponse(200, { objects: [OBJECT_A], next_cursor: '' }))
    renderWithProviders(<ObjectsPage />, { language: 'zh-CN' })
    expect(await screen.findByText('对象存储')).toBeInTheDocument()
    expect(await screen.findByText('已完成')).toBeInTheDocument()
  })
})
