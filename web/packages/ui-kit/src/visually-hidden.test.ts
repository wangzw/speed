
import { describe, expect, it } from 'vitest'
import { visuallyHiddenSx } from './visually-hidden.js'

describe('visuallyHiddenSx', () => {
  it('clips without removing the element from the accessibility tree', () => {
    expect(visuallyHiddenSx).toEqual({
      clip: 'rect(0 0 0 0)',
      clipPath: 'inset(50%)',
      height: '1px',
      width: '1px',
      overflow: 'hidden',
      position: 'absolute',
      bottom: 0,
      left: 0,
      whiteSpace: 'nowrap',
      margin: 0,
    })
  })

  it('never hides through display or visibility', () => {
    expect(visuallyHiddenSx).not.toHaveProperty('display')
    expect(visuallyHiddenSx).not.toHaveProperty('visibility')
  })
})
