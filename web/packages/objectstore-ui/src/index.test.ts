import { describe, expect, it } from 'vitest'
import * as entry from './index.js'

describe('@speed/objectstore-ui public surface', () => {
  it('exports exactly the documented surface', () => {
    // The entry is the package's public API: a consumer relies on these
    // names, and a change to the set is a breaking release. This pins it
    // so an accidental export (or a dropped one) fails loudly.
    expect(Object.keys(entry).sort()).toEqual(
      [
        'OBJECTSTORE_UI_NAMESPACE',
        'ObjectDetailDialog',
        'ObjectsPage',
        'objectstoreUiResources',
      ].sort(),
    )
  })
})
