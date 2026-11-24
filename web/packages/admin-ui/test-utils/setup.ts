/**
 * vitest setup for admin-ui tests: the shared setup plus a desktop-true
 * window.matchMedia default, which jsdom lacks and AppShell's responsive split needs.
 */
import '@speed/test-utils/setup'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { beforeEach } from 'vitest'

beforeEach(() => {
  mockMatchMedia(true)
})
