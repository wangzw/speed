import { describe, expect, it } from 'vitest'
import { deepMerge } from './merge'
import { defaultTokens } from './defaultTokens'
import type { DeepPartial } from './types'

function deepFreeze<T>(value: T): T {
  if (typeof value === 'object' && value !== null) {
    for (const child of Object.values(value as Record<string, unknown>)) {
      deepFreeze(child)
    }
    Object.freeze(value)
  }
  return value
}

function uncheckedPartial<T extends object>(value: unknown): DeepPartial<T> {
  return value as DeepPartial<T>
}

describe('deepMerge', () => {
  it('returns a copy with every override applied at full depth', () => {
    const merged = deepMerge(
      { a: { b: { c: 1, d: 2 } }, e: 'x' },
      { a: { b: { c: 9 } } },
    )
    expect(merged).toEqual({ a: { b: { c: 9, d: 2 } }, e: 'x' })
  })

  it('applies overrides in argument order, later wins', () => {
    const merged = deepMerge(
      { a: 1, b: { c: 1 } },
      { a: 2, b: { c: 2 } },
      { a: 3, b: { c: 3 } },
    )
    expect(merged).toEqual({ a: 3, b: { c: 3 } })
  })

  it('merges a later deep partial onto an earlier override, not onto the base alone', () => {
    const merged = deepMerge(
      { a: { x: 1, y: 1 } },
      { a: { x: 2 } },
      { a: { y: 3 } },
    )
    expect(merged).toEqual({ a: { x: 2, y: 3 } })
  })

  it('skips undefined override values instead of blanking tokens', () => {
    const merged = deepMerge({ a: { b: 1 }, c: 2 }, { a: { b: undefined }, c: undefined })
    expect(merged).toEqual({ a: { b: 1 }, c: 2 })
  })

  it('returns a faithful copy of the base when an override omits keys', () => {
    const base: { a: { b: number | undefined; c: number }; d: string | undefined } = {
      a: { b: undefined, c: 1 },
      d: undefined,
    }
    const merged = deepMerge(base, { a: { c: 2 } })
    expect('d' in merged).toBe(true)
    expect('b' in merged.a).toBe(true)
    expect(merged.a.c).toBe(2)
    expect(merged.a.b).toBeUndefined()
    const untouched = deepMerge(base)
    expect('d' in untouched).toBe(true)
    expect('b' in untouched.a).toBe(true)
    expect(untouched.a).toBe(base.a)
  })

  it('replaces object fields wholesale when the override value is not a plain record', () => {
    expect(deepMerge({ a: [1, 2] }, { a: [3] })).toEqual({ a: [3] })
    interface Sample {
      a: { b: number }
    }
    expect(deepMerge({ a: { b: 1 } }, uncheckedPartial<Sample>({ a: null }))).toEqual({
      a: null,
    })
    expect(deepMerge({ a: { b: 1 } }, uncheckedPartial<Sample>({ a: 0 }))).toEqual({ a: 0 })
    expect(deepMerge({ a: { b: 1 } }, uncheckedPartial<Sample>({ a: 'x' }))).toEqual({
      a: 'x',
    })
    expect(deepMerge({ a: { b: 1 } }, uncheckedPartial<Sample>({ a: false }))).toEqual({
      a: false,
    })
  })

  it('never mutates any input, even when inputs are deeply frozen', () => {
    const base = deepFreeze({ color: { main: '#111111', light: '#222222' }, ok: true })
    const override = deepFreeze({ color: { main: '#333333' } })
    const merged = deepMerge(base, override)
    expect(merged).toEqual({ color: { main: '#333333', light: '#222222' }, ok: true })
    expect(base).toEqual({ color: { main: '#111111', light: '#222222' }, ok: true })
  })

  it('overrides one surface role and leaves the sibling roles shared', () => {
    const merged = deepMerge(defaultTokens, {
      surface: { container: { fill: 'rgba(255, 255, 255, 0.72)' } },
    })
    expect(merged.surface.container.fill).toBe('rgba(255, 255, 255, 0.72)')
    expect(merged.surface.container.borderWidth).toBe(1)
    expect(merged.surface).not.toBe(defaultTokens.surface)
    expect(merged.surface.container).not.toBe(defaultTokens.surface.container)
    expect(merged.surface.page).toBe(defaultTokens.surface.page)
    expect(merged.surface.overlay).toBe(defaultTokens.surface.overlay)
    expect(merged.surface.header).toBe(defaultTokens.surface.header)
    expect(defaultTokens.surface.container.fill).toBeUndefined()
  })

  it('leaves surface untouched when an override only changes background.paper', () => {
    const merged = deepMerge(defaultTokens, {
      color: { background: { paper: '#FEFFFE' } },
    })
    expect(merged.color.background.paper).toBe('#FEFFFE')
    expect(merged.surface).toBe(defaultTokens.surface)
  })

  it('shares untouched branches with the base by identity and rebuilds touched ones', () => {
    const merged = deepMerge(defaultTokens, { color: { divider: '#EEEEEE' } })
    expect(merged.typography).toBe(defaultTokens.typography)
    expect(merged.color).not.toBe(defaultTokens.color)
    expect(merged.color.semantic).toBe(defaultTokens.color.semantic)
    expect(merged.color.divider).toBe('#EEEEEE')
  })

  it('keeps base objects whose path no override enters untouched (no deep clone)', () => {
    const base = { a: { b: 1 }, deep: { x: { y: 1 } } }
    const merged = deepMerge(base, { a: { b: 2 } })
    expect(merged.deep).toBe(base.deep)
    expect(merged).toEqual({ a: { b: 2 }, deep: { x: { y: 1 } } })
  })

  it('stores a hostile "__proto__" override key as a non-enumerable own data property', () => {
    const hostile = JSON.parse('{"__proto__": {"polluted": true}, "a": {"b": 1}}') as {
      a: { b: number }
    }
    const merged = deepMerge({ a: { b: 0 } }, hostile as { a: { b: number } }) as Record<
      string,
      unknown
    >
    expect(Object.getPrototypeOf(merged)).toBe(Object.prototype)
    expect(Object.prototype.hasOwnProperty.call(merged, '__proto__')).toBe(true)
    expect(merged['__proto__']).toEqual({ polluted: true })
    expect((Object.prototype as { polluted?: boolean }).polluted).toBeUndefined()
    expect(merged.a).toEqual({ b: 1 })
  })

  it('never lets a hostile "__proto__" payload become an enumerable own key', () => {
    const first = JSON.parse('{"__proto__": {"a": 1}}') as Record<string, unknown>
    const second = JSON.parse('{"__proto__": {"b": 2}}') as Record<string, unknown>
    const merged = deepMerge(first, second) as Record<string, unknown>
    expect(Object.prototype.hasOwnProperty.call(merged, '__proto__')).toBe(true)
    expect(merged['__proto__']).toEqual({ a: 1, b: 2 })
    expect(Object.keys(merged)).toEqual([])
    expect(Object.getPrototypeOf(merged)).toBe(Object.prototype)
    expect((Object.prototype as { a?: unknown; b?: unknown }).a).toBeUndefined()
    expect((Object.prototype as { a?: unknown; b?: unknown }).b).toBeUndefined()
  })

  it('keeps the merged result safe for downstream [[Set]]-based copies', () => {
    const hostile = JSON.parse('{"__proto__": {"polluted": true}}') as { a?: number }
    const merged = deepMerge({ a: 1 }, hostile)
    const copy = {} as { polluted?: boolean }
    Object.assign(copy, merged)
    expect(Object.getPrototypeOf(copy)).toBe(Object.prototype)
    expect(copy.polluted).toBeUndefined()
    expect((Object.prototype as { polluted?: boolean }).polluted).toBeUndefined()
    const spread = { ...merged } as { polluted?: boolean }
    expect(spread.polluted).toBeUndefined()
  })

  it('keeps function-typed fields whole at compile time: only a function may override a function', () => {
    interface WithFormatter {
      format: (value: number) => string
    }
    // @ts-expect-error a number cannot stand in for a function-typed field
    deepMerge<WithFormatter>({ format: (value: number) => String(value) }, { format: 42 })
    const merged = deepMerge<WithFormatter>(
      { format: (value: number) => String(value) },
      { format: (value: number) => `#${value}` },
    )
    expect(merged.format(7)).toBe('#7')
  })
})
