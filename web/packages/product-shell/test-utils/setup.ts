import '@speed/test-utils/setup'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { beforeEach } from 'vitest'

beforeEach(() => {
  mockMatchMedia(true)
})
