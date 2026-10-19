/**
 * useObjectDownload over the objectstore content route.
 *
 * The hook returns the operation a surface invokes for one row's download:
 * read GET /objects/{id}/content as a Blob and hand it to the browser
 * under the derived filename. These tests pin the public orchestration a
 * host composes directly, with the save stubbed so the suite asserts the
 * bytes and the name instead of a jsdom navigation.
 */

import { describe, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import type { ObjectstoreObject } from '@speed/api-sdk'
import { renderHookWithProviders } from '../test-utils/render.js'
import {
  blobResponse,
  makeObjectStoreRig,
} from '../test-utils/real-client.js'
import { useObjectDownload } from './downloads.js'

vi.mock('./internal/download.js', () => ({ saveBlob: vi.fn() }))
import { saveBlob } from './internal/download.js'

const LIST_PATH = '/api/v1/objects'

const OBJECT: ObjectstoreObject = {
  id: 'o-aaa',
  name: 'greeting.txt',
  state: 'completed',
  size: 9,
  mime: 'text/plain',
  checksum_sha256: 'aaaa',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-02T00:00:00Z',
}

describe('useObjectDownload', () => {
  it("saves the fetched bytes under the object's stored name", async () => {
    vi.mocked(saveBlob).mockClear()
    makeObjectStoreRig((call) => {
      if (call.method === 'GET' && call.path === `${LIST_PATH}/o-aaa/content`) {
        return blobResponse('the-bytes', 'text/plain')
      }
      throw new Error(`unexpected request ${call.method} ${call.path}`)
    })
    const { result } = renderHookWithProviders(() => useObjectDownload(), {
      language: 'en-US',
    })

    await act(async () => {
      await result.current(OBJECT)
    })

    expect(saveBlob).toHaveBeenCalledTimes(1)
    const [blob, filename] = vi.mocked(saveBlob).mock.calls[0] as [Blob, string]
    expect(await blob.text()).toBe('the-bytes')
    expect(filename).toBe('greeting.txt')
  })

  it('derives the name from the id and MIME when the object is unnamed', async () => {
    vi.mocked(saveBlob).mockClear()
    makeObjectStoreRig((call) => {
      if (call.method === 'GET' && call.path === `${LIST_PATH}/o-aaa/content`) {
        return blobResponse('the-bytes', 'text/plain')
      }
      throw new Error(`unexpected request ${call.method} ${call.path}`)
    })
    const { result } = renderHookWithProviders(() => useObjectDownload(), {
      language: 'en-US',
    })

    await act(async () => {
      await result.current({ ...OBJECT, name: '' })
    })

    expect(saveBlob).toHaveBeenCalledTimes(1)
    const [, filename] = vi.mocked(saveBlob).mock.calls[0] as [Blob, string]
    expect(filename).toBe('o-aaa.txt')
  })
})
