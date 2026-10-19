#!/usr/bin/env node
/**
 * Feature-name generator: turns the host's feature vocabulary
 * (contracts/features.json, written by the demo host's `gen --features`)
 * into the generated feature-name surface.
 *
 * Two copies of this script exist. The platform entry
 * (web/scripts/feature-names.mjs) writes the @speed/api-sdk package's
 * src/features.ts from the platform-owned entries, and optionally the
 * application's module augmentation; it runs from web/package.json's
 * `api:gen`. The demo starter's copy
 * (starters/demo/web/scripts/feature-names.mjs) writes only the
 * application's module augmentation into the app's own src/app-api/; it
 * runs from starters/demo/web/package.json's `api:gen`. Everything
 * outside the generation-mode block below is byte-identical between the
 * two copies, and web/scripts/starter-script-consistency.test.mjs pins
 * that.
 *
 * Usage of the platform entry, with paths relative to the working
 * directory:
 *
 *   node web/scripts/feature-names.mjs \
 *       contracts/features.json \
 *       web/packages/api-sdk/src/features.ts \
 *       [src/app-api/features.ts]
 *
 * Every vocabulary entry carries an owner the demo host's `gen` writes
 * mechanically from the declaring module's Go package: `platform` for a
 * module the platform delivers, `application` for the host's own. The
 * first output registers only platform-owned names, so the published
 * package never carries an application's vocabulary; the second output
 * (optional) declares the application-owned names as a module
 * augmentation of the same registry, which is how the application's own
 * code keeps type-checking against the names its own modules declare.
 *
 * The committed outputs are generated artifacts: edit this script or the
 * vocabulary, never the files it writes. On the platform side,
 * `pnpm api:gen` from web/ regenerates src/features.ts (and the platform
 * orval output); on the application side, `pnpm api:gen` from
 * starters/demo/web runs the demo host's `gen`, which writes the
 * vocabulary beside the merged OpenAPI document, and then runs the
 * starter's copy of this script. A declaration added, removed or renamed
 * on the backend reaches the frontend's types through those commands.
 *
 * The output depends on the vocabulary's contents alone, not on the
 * argument's spelling: the header names the contract by its canonical
 * path, so running the script from any directory with any relative path
 * writes the same bytes.
 */
import { readFileSync, writeFileSync } from 'node:fs'

// --- generation mode (the one documented difference between the two copies) ---
// The generator's own name and the command that reruns it are stamped into
// every artifact's header, so each output names the script and the command
// that actually own it (the app artifact is this copy, regenerated from the
// project root).
const GENERATOR = 'scripts/feature-names.mjs'
const REGENERATE = '`pnpm api:gen` from the project root'

const [inputPath, applicationOutputPath] = process.argv.slice(2)
if (!inputPath || !applicationOutputPath) {
  console.error(
    'usage: feature-names.mjs <contracts/features.json> <application-features.ts>',
  )
  process.exit(1)
}

// App mode: this copy writes the application's module augmentation only.
// It never receives -- and so never needs a writable path for -- the
// platform package's features.ts, whose owner is web/package.json's api:gen.
function writeOutputs(_platformBody, applicationBody) {
  writeFileSync(applicationOutputPath, applicationBody)
}
// --- end generation mode ---

let vocabulary
try {
  vocabulary = JSON.parse(readFileSync(inputPath, 'utf8'))
} catch (error) {
  console.error(`feature-names: reading ${inputPath}: ${error.message}`)
  process.exit(1)
}

if (!Array.isArray(vocabulary.features)) {
  console.error(`feature-names: ${inputPath} carries no "features" array`)
  process.exit(1)
}

const OWNERS = ['platform', 'application']

const entries = vocabulary.features.map((entry, index) => {
  const name = entry?.name
  if (typeof name !== 'string' || !/^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$/.test(name)) {
    console.error(
      `feature-names: ${inputPath}: entry ${index} carries no valid feature name`,
    )
    process.exit(1)
  }
  const owner = entry?.owner
  if (!OWNERS.includes(owner)) {
    console.error(
      `feature-names: ${inputPath}: entry ${index} (${name}) carries owner ${JSON.stringify(owner)}, ` +
        `want one of ${OWNERS.map((value) => JSON.stringify(value)).join(', ')}`,
    )
    process.exit(1)
  }
  return { name, owner }
})

// The backend writes the vocabulary sorted by name; sorting here as well
// keeps the artifact deterministic against a hand-edited input.
entries.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
for (let i = 1; i < entries.length; i += 1) {
  if (entries[i].name === entries[i - 1].name) {
    console.error(`feature-names: ${inputPath} declares ${entries[i].name} twice`)
    process.exit(1)
  }
}

const platformNames = entries
  .filter((entry) => entry.owner === 'platform')
  .map((entry) => entry.name)
const applicationNames = entries
  .filter((entry) => entry.owner === 'application')
  .map((entry) => entry.name)

const header = `// Code generated by ${GENERATOR}. DO NOT EDIT.
// Source: contracts/features.json, written by the demo host's gen command
// (--features) from the feature.Spec declarations the binary's imports
// reach. Regenerate with ${REGENERATE}; never edit this file by hand.
`

const registry = platformNames
  .map((name) => `  '${name}': true`)
  .join('\n')
const list = platformNames.map((name) => `  '${name}',`).join('\n')

const body = `${header}
/**
 * Every feature name the platform can declare, registered one per key.
 * The interface is augmentable: an application appends the names its own
 * modules declare to it, and FeatureName picks them up.
 */
export interface FeatureNameRegistry {
${registry}
}

/** The name of a feature a frontend surface may require. */
export type FeatureName = keyof FeatureNameRegistry & string

/** Every platform feature name, ascending. */
export const FEATURE_NAMES: readonly FeatureName[] = [
${list}
]
`

// A module augmentation, not an ambient declaration: the file has an
// export, so `declare module '@speed/api-sdk/features'` merges into the
// package's registry instead of replacing the module.
const applicationRegistry = applicationNames
  .map((name) => `    '${name}': true`)
  .join('\n')
const augmentation =
  applicationNames.length === 0
    ? ''
    : `
declare module '@speed/api-sdk/features' {
  interface FeatureNameRegistry {
${applicationRegistry}
  }
}
`
const applicationBody = `${header}
// The application-owned half of the feature vocabulary: the names the
// host's own modules declare. They enter this application's FeatureName
// through the registry augmentation below and never the published
// package's registry or FEATURE_NAMES.
${augmentation}
export {}
`

writeOutputs(body, applicationBody)
