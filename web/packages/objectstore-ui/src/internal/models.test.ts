import { describe, expect, it } from 'vitest'
import { asObjectDetail, asObjectPage } from './models.js'

describe('response narrowing', () => {
  it('passes an object body through unchanged', () => {
    const page = { objects: [], next_cursor: 'C' }
    expect(asObjectPage(page)).toEqual(page)
    const detail = { object: { id: 'o-1' } }
    expect(asObjectDetail(detail)).toEqual(detail)
  })

  it('reads an absent or non-object body as an empty object', () => {
    // A 2xx whose body omitted the member reads as undefined downstream,
    // the caller's error variant, rather than as a valid empty page.
    expect(asObjectPage(undefined)).toEqual({})
    expect(asObjectDetail(null)).toEqual({})
    expect(asObjectDetail('not-a-document')).toEqual({})
  })
})
