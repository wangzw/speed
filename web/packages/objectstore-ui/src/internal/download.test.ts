import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { saveBlob } from './download.js'

/** The object-URL methods, which jsdom may or may not provide. */
type UrlGlobal = {
  createObjectURL?: (blob: Blob) => string
  revokeObjectURL?: (url: string) => void
}
const urlGlobal = URL as unknown as UrlGlobal

const originalCreate = urlGlobal.createObjectURL
const originalRevoke = urlGlobal.revokeObjectURL

describe('saveBlob', () => {
  beforeEach(() => {
    // jsdom does not implement a real navigation: the anchor click is a
    // no-op under test, spied so the suite can observe it fired.
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    urlGlobal.createObjectURL = originalCreate
    urlGlobal.revokeObjectURL = originalRevoke
  })

  it('hands the bytes to an anchor with the download name and revokes the URL', () => {
    const createObjectURL = vi.fn(() => 'blob:mock-url')
    const revokeObjectURL = vi.fn()
    urlGlobal.createObjectURL = createObjectURL
    urlGlobal.revokeObjectURL = revokeObjectURL

    const blob = new Blob(['bytes'], { type: 'text/plain' })
    saveBlob(blob, 'o-1.txt')

    expect(createObjectURL).toHaveBeenCalledWith(blob)
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-url')
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
    // The synthetic anchor does not linger in the document.
    expect(document.querySelector('a[download="o-1.txt"]')).toBeNull()
  })

  it('still clicks an inert anchor when the object URL API is absent', () => {
    urlGlobal.createObjectURL = undefined
    saveBlob(new Blob(['bytes']), 'o-2.bin')
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
  })
})
