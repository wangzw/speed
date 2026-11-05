/**
 * The app-owned SDK's runtime seam (src/app-api/), the hand-written file beside
 * the orval-generated src/app-api/index.ts. It re-exports the mutators from
 * @speed/api-sdk/runtime, so the app's own generated operations reach the
 * backend through the same seam the platform's generated surface uses: one
 * `bindRequestFn(createClient(...))` call at bootstrap serves both levels, and
 * no generated or bridge code performs HTTP itself.
 *
 * The seam exists as a file of its own because orval emits the mutator
 * import as a sibling relative path (`./runtime`), which
 * scripts/orval-nodenext-fixup.mjs rewrites to './runtime.js' after
 * every regeneration -- the same shape the platform package's
 * src/runtime.ts has. Regeneration covers src/app-api/index.ts only,
 * never this file.
 *
 * The app owns two operations of its own today: the host's personal-tenant
 * ensure (`POST /api/v1/orgs/personal`), generated from the contract, and
 * the review build's notice control (src/app-api/review-notice.ts), which is
 * hand-written because the review-only route is not in the baseline contract.
 * The generated half needs no mutator the platform package does not export.
 */

export {
  speedRequest,
  speedRequestCredentialless,
} from '@speed/api-sdk/runtime'
