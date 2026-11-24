import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach } from 'vitest'
import { asyncBudgetMs, gateTimeScale } from './gate-time.js'

configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })

afterEach(() => {
  cleanup()
})

// jsdom's getContext stub returns null but also logs a jsdomError, which
// axe-core's icon-ligature probe triggers on every scan; answer null
// directly so the verdict is unchanged and the noise is gone.
if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = (() =>
    null) as unknown as HTMLCanvasElement['getContext']
}
