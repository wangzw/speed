import { describe, expect, it } from 'vitest'
import * as tokens from './index'

describe('@speed/tokens entry', () => {
  it('exports exactly defaultTokens and deepMerge at runtime', () => {
    expect(Object.keys(tokens).sort()).toEqual(['deepMerge', 'defaultTokens'])
  })

  it('exports the default token tree', () => {
    expect(tokens.defaultTokens.color.semantic.primary.main).toBe('#2563EB')
    expect(typeof tokens.defaultTokens).toBe('object')
  })

  it('exports a function-valued deepMerge', () => {
    expect(typeof tokens.deepMerge).toBe('function')
  })
})
