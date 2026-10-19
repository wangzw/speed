/**
 * useObjectUploads over the objectstore routes.
 *
 * The hook owns the FileUploader queue: a picked file runs the module's
 * three-step transfer (declare -> upload the bytes -> complete) and the
 * row settles succeeded. These tests pin the public orchestration a host
 * composes directly: the completed object and the File that produced it
 * reach the latest `onUploaded` callback, after the transfer's three
 * requests have landed.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, waitFor } from '@testing-library/react'
import type { ObjectstoreObject } from '@speed/api-sdk'
import { renderHookWithProviders } from '../test-utils/render.js'
import {
  jsonResponse,
  makeObjectStoreRig,
  noContentResponse,
} from '../test-utils/real-client.js'
import type { Responder } from '../test-utils/real-client.js'
import { useObjectUploads } from './uploads.js'

const LIST_PATH = '/api/v1/objects'

const PENDING_OBJECT: ObjectstoreObject = {
  id: 'o-new',
  name: 'greeting.txt',
  state: 'pending',
  size: 5,
  mime: 'text/plain',
  checksum_sha256: '',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

const COMPLETED_OBJECT: ObjectstoreObject = {
  ...PENDING_OBJECT,
  state: 'completed',
  checksum_sha256: 'aaaa',
}

/** The three-step responder a successful transfer expects. */
function transferResponder(call: Parameters<Responder>[0]): Response {
  if (call.method === 'GET' && call.path === LIST_PATH) {
    return jsonResponse(200, { objects: [], next_cursor: '' })
  }
  if (call.method === 'POST' && call.path === LIST_PATH) {
    return jsonResponse(201, { object: PENDING_OBJECT })
  }
  if (call.method === 'PUT' && call.path === `${LIST_PATH}/o-new/content`) {
    return noContentResponse()
  }
  if (call.method === 'POST' && call.path === `${LIST_PATH}/o-new/complete`) {
    return jsonResponse(200, { object: COMPLETED_OBJECT })
  }
  throw new Error(`unexpected request ${call.method} ${call.path}`)
}

describe('useObjectUploads', () => {
  it('reports the completed object and its File through onUploaded', async () => {
    const onUploaded = vi.fn()
    const rig = makeObjectStoreRig(transferResponder)
    const { result } = renderHookWithProviders(
      (props: { readonly onUploaded: typeof onUploaded }) =>
        useObjectUploads({ onUploaded: props.onUploaded }),
      { initialProps: { onUploaded }, language: 'en-US' },
    )

    const file = new File(['hello'], 'greeting.txt', { type: 'text/plain' })
    act(() => {
      result.current.selectFiles([file])
    })

    await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1))
    // The payload is the response object of the completing call, not the
    // pending declaration the transfer started from.
    expect(onUploaded).toHaveBeenCalledWith(COMPLETED_OBJECT, file)
    expect(
      rig.calls.map((call) => `${call.method} ${call.path}`),
    ).toEqual(
      expect.arrayContaining([
        `POST ${LIST_PATH}`,
        `PUT ${LIST_PATH}/o-new/content`,
        `POST ${LIST_PATH}/o-new/complete`,
      ]),
    )
    await waitFor(() => {
      expect(result.current.rows[0]?.status).toBe('succeeded')
    })
  })

  it('invokes the latest onUploaded when the host re-renders mid-transfer', async () => {
    const first = vi.fn()
    const second = vi.fn()
    // The completing call is held open so the host can re-render while the
    // transfer is already in flight. The callback is chosen when the
    // transfer ends, not when it started: a closure that read the options
    // of the starting render would still call `first`.
    let releaseComplete!: () => void
    const completeGate = new Promise<void>((resolve) => {
      releaseComplete = resolve
    })
    const rig = makeObjectStoreRig((call) => {
      if (call.method === 'POST' && call.path === `${LIST_PATH}/o-new/complete`) {
        return completeGate.then(() =>
          jsonResponse(200, { object: COMPLETED_OBJECT }),
        )
      }
      return transferResponder(call)
    })
    const { result, rerender } = renderHookWithProviders(
      (props: { readonly onUploaded: (object: ObjectstoreObject, file: File) => void }) =>
        useObjectUploads({ onUploaded: props.onUploaded }),
      { initialProps: { onUploaded: first }, language: 'en-US' },
    )

    const file = new File(['hello'], 'greeting.txt', { type: 'text/plain' })
    act(() => {
      result.current.selectFiles([file])
    })
    // The transfer has reached its last leg and waits on the answer.
    await waitFor(() =>
      expect(
        rig.calls.filter((call) => call.path === `${LIST_PATH}/o-new/complete`),
      ).toHaveLength(1),
    )

    rerender({ onUploaded: second })
    act(() => {
      releaseComplete()
    })

    await waitFor(() => expect(second).toHaveBeenCalledTimes(1))
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledWith(COMPLETED_OBJECT, file)
    // The re-render neither restarted the queue nor dropped the transfer
    // already in flight: exactly one declaration was made.
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === LIST_PATH,
      ),
    ).toHaveLength(1)
    await waitFor(() => expect(result.current.rows[0]?.status).toBe('succeeded'))
  })

  it('keeps the row succeeded when onUploaded throws', async () => {
    const onUploaded = vi.fn(() => {
      throw new Error('host callback failed')
    })
    makeObjectStoreRig(transferResponder)
    const { result } = renderHookWithProviders(
      (props: { readonly onUploaded: typeof onUploaded }) =>
        useObjectUploads({ onUploaded: props.onUploaded }),
      { initialProps: { onUploaded }, language: 'en-US' },
    )

    const file = new File(['hello'], 'greeting.txt', { type: 'text/plain' })
    act(() => {
      result.current.selectFiles([file])
    })

    await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1))
    // The transfer settled before the callback ran, and the callback's
    // failure stops at its boundary: it neither reopens the row nor leaves
    // an unhandled rejection behind (vitest fails the run on one).
    expect(result.current.rows[0]?.status).toBe('succeeded')
  })
})
