/**
 * api-sdk test configuration: the generated react-query hooks are
 * exercised through @testing-library/react's renderHook, which mounts a
 * host component into a DOM -- hence the jsdom environment. No aliases
 * are needed here: package sources import only relative files plus the
 * @speed/api-client type surface (type-only imports, erased at runtime),
 * and react-query/react resolve from the package's own devDependencies.
 * Timeout and the shared coverage block come from the workspace preset
 * (web/vitest.shared.ts); the one coverage difference is the generated
 * output's exclusion below.
 */
import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from '../../vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions({
    environment: 'jsdom',
    // The one file left out of src/** is src/index.ts, the
    // orval-generated output (its DO-NOT-EDIT header); the hand-written
    // src/runtime.ts seam stays measured, and the shared 80%
    // statements/branches floor applies to that measured set.
    coverage: {
      exclude: ['src/index.ts'],
    },
  }),
})
