import { describe, expect, it } from 'vitest'
import { stateLabelKey } from './status.js'

describe('stateLabelKey', () => {
  it('maps each known state to its own bundle key', () => {
    expect(stateLabelKey('pending')).toBe('state.pending')
    expect(stateLabelKey('uploaded')).toBe('state.uploaded')
    expect(stateLabelKey('completed')).toBe('state.completed')
    expect(stateLabelKey('deleted')).toBe('state.deleted')
  })

  it('collapses an unknown state to the generic label, never the raw value', () => {
    expect(stateLabelKey('archived')).toBe('state.other')
    expect(stateLabelKey('')).toBe('state.other')
  })
})
