import { describe, expect, it } from 'vitest'
import {
  downloadFilename,
  extensionForMime,
  formatByteNumber,
} from './format.js'

describe('formatByteNumber', () => {
  it('formats through Intl.NumberFormat, localizing the grouping', () => {
    // The number comes from Intl, never a hand-rolled string; en-US
    // groups thousands with a comma, and the caller supplies the unit.
    expect(formatByteNumber(1024, 'en-US')).toBe('1,024')
    expect(formatByteNumber(5, 'en-US')).toBe('5')
  })
})

describe('extensionForMime', () => {
  it('maps common MIME types to conventional extensions', () => {
    expect(extensionForMime('image/png')).toBe('.png')
    expect(extensionForMime('image/jpeg')).toBe('.jpg')
    expect(extensionForMime('text/plain')).toBe('.txt')
    expect(extensionForMime('application/json')).toBe('.json')
    expect(extensionForMime('text/plain; charset=utf-8')).toBe('.txt')
  })

  it('falls back to .bin when no extension is derivable', () => {
    expect(extensionForMime('')).toBe('.bin')
    expect(extensionForMime('application/octet-stream')).toBe('.bin')
    expect(extensionForMime('weird')).toBe('.bin')
  })
})

describe('downloadFilename', () => {
  it('names the file after the stored name when there is one', () => {
    expect(downloadFilename('greeting.txt', 'o-1', 'image/png')).toBe(
      'greeting.txt',
    )
    expect(
      downloadFilename('archive.tar.gz', 'o-2', 'application/octet-stream'),
    ).toBe('archive.tar.gz')
  })

  it('falls back to the object id with a MIME-derived extension when unnamed', () => {
    expect(downloadFilename('', 'o-1', 'image/png')).toBe('o-1.png')
    expect(downloadFilename('', 'o-2', 'application/octet-stream')).toBe(
      'o-2.bin',
    )
  })
})
