/**
 * Rule tests for no-double-feedback-region. Run from the web workspace
 * root:
 *
 *   pnpm exec vitest run eslint-rules/no-double-feedback-region.test.mjs
 *
 * The RuleTester cases pin the rule's judgement; the final suite scans
 * every runtime TSX file under web/packages/<name>/src and the demo
 * host's src -- the scopes the workspace and host lint configs give the
 * rule -- and holds the exemption list exactly equal to the multi-region
 * components in either tree, so a new one cannot slip in unregistered and
 * a stale entry cannot linger.
 *
 * Test snippets are fixture code for the rule under test; the scanned
 * files are the packages' and the host's own source.
 */

import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { RuleTester } from 'eslint'
import tseslint from 'typescript-eslint'
import { describe, expect, it } from 'vitest'
import {
  WRITE_FEEDBACK_REGION_EXEMPTIONS,
  collectRegionComponents,
  noDoubleFeedbackRegionRule,
} from './no-double-feedback-region.js'

const ruleTester = new RuleTester({
  languageOptions: {
    parserOptions: {
      ecmaFeatures: { jsx: true },
      sourceType: 'module',
    },
  },
})

/**
 * RuleTester.run throws an aggregate error listing every failing case;
 * surface that error as the test failure instead of swallowing it.
 */
function runRule(valid, invalid) {
  try {
    ruleTester.run('no-double-feedback-region', noDoubleFeedbackRegionRule, {
      valid,
      invalid,
    })
  } catch (error) {
    expect.fail(`rule tests failed: ${error.message}`)
  }
}

const SOCIAL_BINDINGS_FILE =
  'packages/account-ui/src/SocialBindingsSection.tsx'
const ACCOUNTS_PAGE_FILE = 'packages/admin-ui/src/AccountsPage.tsx'
const HOST_CONTROL_FILE =
  'starters/demo/web/src/review-notice-control.tsx'

describe('no-double-feedback-region rule', () => {
  it('accepts one region per component', () => {
    runRule(
      [
        // One control group, one scope, one region.
        'function Panel() { return <WriteFeedbackRegion outcome={feedback.outcome} /> }',
        // Two components in one file each render their own region.
        'function First() { return <WriteFeedbackRegion outcome={a.outcome} /> }' +
          'function Second() { return <WriteFeedbackRegion outcome={b.outcome} /> }',
        // An anonymous render prop belongs to the component that declares it.
        'function Table() { return <AsyncSection>{() => <WriteFeedbackRegion outcome={feedback.outcome} />}</AsyncSection> }',
        // A per-row result that is not a WriteFeedbackRegion stays legal:
        // an upload queue row carries its status as text from row props.
        'function Uploader({ rows }) { return <ul>{rows.map((row) => <li key={row.id}>{row.status}</li>)}</ul> }',
        // One region rendered per row is one JSX element in the component.
        'function Uploader({ rows }) { return <ul>{rows.map((row) => <li key={row.id}><WriteFeedbackRegion outcome={row.outcome} /></li>)}</ul> }',
      ],
      [],
    )
  })

  it('accepts a registered multi-region component by file and name', () => {
    runRule(
      [
        {
          filename: ACCOUNTS_PAGE_FILE,
          code:
            'function AccountsPage() { return <><WriteFeedbackRegion outcome={list.outcome} /><WriteFeedbackRegion outcome={detail.outcome} /></> }',
        },
      ],
      [],
    )
  })

  it('flags two regions in one component, naming the file and count', () => {
    runRule(
      [],
      [
        {
          filename: SOCIAL_BINDINGS_FILE,
          code:
            'function Bindings() { return <><WriteFeedbackRegion outcome={unbind.outcome} /><WriteFeedbackRegion outcome={authorize.outcome} /></> }',
          errors: [
            {
              messageId: 'doubleRegion',
              data: {
                file: SOCIAL_BINDINGS_FILE,
                component: 'Bindings',
                count: 2,
              },
            },
          ],
        },
      ],
    )
  })

  it('reports the total count when a component renders more than two', () => {
    runRule(
      [],
      [
        {
          filename: SOCIAL_BINDINGS_FILE,
          code:
            'function Bindings() { return <><WriteFeedbackRegion outcome={a.outcome} /><WriteFeedbackRegion outcome={b.outcome} /><WriteFeedbackRegion outcome={c.outcome} /></> }',
          errors: [
            {
              messageId: 'doubleRegion',
              data: {
                file: SOCIAL_BINDINGS_FILE,
                component: 'Bindings',
                count: 3,
              },
            },
          ],
        },
      ],
    )
  })

  it('does not exempt another component in a registered file', () => {
    runRule(
      [],
      [
        {
          filename: ACCOUNTS_PAGE_FILE,
          code:
            'function BrandNewPanel() { return <><WriteFeedbackRegion outcome={a.outcome} /><WriteFeedbackRegion outcome={b.outcome} /></> }',
          errors: [
            {
              messageId: 'doubleRegion',
              data: {
                file: ACCOUNTS_PAGE_FILE,
                component: 'BrandNewPanel',
                count: 2,
              },
            },
          ],
        },
      ],
    )
  })

  it('follows an aliased import of the region', () => {
    runRule(
      [],
      [
        {
          filename: SOCIAL_BINDINGS_FILE,
          code:
            "import { WriteFeedbackRegion as Region } from '@speed/ui-kit'\n" +
            'function Bindings() { return <><Region outcome={a.outcome} /><Region outcome={b.outcome} /></> }',
          errors: [
            {
              messageId: 'doubleRegion',
              data: {
                file: SOCIAL_BINDINGS_FILE,
                component: 'Bindings',
                count: 2,
              },
            },
          ],
        },
      ],
    )
  })

  it('counts the shared wrapper the internal region modules render', () => {
    runRule(
      [],
      [
        {
          filename: SOCIAL_BINDINGS_FILE,
          code:
            'function Bindings() { return <><SharedWriteFeedbackRegion outcome={a.outcome} /><SharedWriteFeedbackRegion outcome={b.outcome} /></> }',
          errors: [
            {
              messageId: 'doubleRegion',
              data: {
                file: SOCIAL_BINDINGS_FILE,
                component: 'Bindings',
                count: 2,
              },
            },
          ],
        },
      ],
    )
  })

  it('flags two regions in a demo host component by its repository path', () => {
    runRule(
      [],
      [
        {
          filename: HOST_CONTROL_FILE,
          code:
            'function ReviewNoticeControl() { return <><WriteFeedbackRegion outcome={a.outcome} /><WriteFeedbackRegion outcome={b.outcome} /></> }',
          errors: [
            {
              messageId: 'doubleRegion',
              data: {
                file: HOST_CONTROL_FILE,
                component: 'ReviewNoticeControl',
                count: 2,
              },
            },
          ],
        },
      ],
    )
  })

  it('spells the file and the count in the message', () => {
    runRule(
      [],
      [
        {
          filename: SOCIAL_BINDINGS_FILE,
          code:
            'function Bindings() { return <><WriteFeedbackRegion outcome={a.outcome} /><WriteFeedbackRegion outcome={b.outcome} /></> }',
          errors: [
            {
              message:
                'packages/account-ui/src/SocialBindingsSection.tsx renders 2 WriteFeedbackRegion elements in component Bindings. One feedback scope renders one region for its control group, so two regions can show two results at once. Make the groups share one scope, move the extra group into its own component, or register this component in WRITE_FEEDBACK_REGION_EXEMPTIONS with a reason.',
            },
          ],
        },
      ],
    )
  })
})

/** Every TSX file under one package's src; the workspace lint config
 * applies the rule to each package's src and excludes test files and
 * `test-utils/`, so the scan skips the same entries. */
function runtimeTsxFiles(directory, found = []) {
  for (const entry of readdirSync(directory)) {
    if (
      entry === 'node_modules' ||
      entry === 'dist' ||
      entry === 'test-utils'
    ) {
      continue
    }
    const full = join(directory, entry)
    if (statSync(full).isDirectory()) {
      runtimeTsxFiles(full, found)
      continue
    }
    if (
      entry.endsWith('.tsx') &&
      !entry.endsWith('.test.tsx') &&
      !entry.endsWith('.spec.tsx')
    ) {
      found.push(full)
    }
  }
  return found
}

function parseProgram(file) {
  const { ast } = tseslint.parser.parseForESLint(readFileSync(file, 'utf8'), {
    filePath: file,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
    range: true,
    loc: true,
  })
  return ast
}

describe('no-double-feedback-region exemptions', () => {
  const webRoot = fileURLToPath(new URL('..', import.meta.url))
  const packagesRoot = join(webRoot, 'packages')
  const hostSrcRoot = join(webRoot, '..', 'starters/demo/web/src')

  /** The `src` directory of every workspace package that has one. */
  function packageSrcDirs() {
    return readdirSync(packagesRoot)
      .map((name) => join(packagesRoot, name, 'src'))
      .filter((dir) => existsSync(dir) && statSync(dir).isDirectory())
  }

  /** Every runtime source root the two lint configs give the rule, with
   * the prefix its exemption entries use: a package keys from `web/`, the
   * demo host from the repository root. */
  function sourceScopes() {
    return [
      ...packageSrcDirs().map((dir) => ({
        root: dir,
        prefix: `packages/${relative(packagesRoot, dir).split('\\').join('/')}/`,
      })),
      { root: hostSrcRoot, prefix: 'starters/demo/web/src/' },
    ]
  }

  function multiRegionComponents() {
    const components = []
    for (const scope of sourceScopes()) {
      if (!existsSync(scope.root) || !statSync(scope.root).isDirectory()) {
        continue
      }
      for (const file of runtimeTsxFiles(scope.root)) {
        for (const entry of collectRegionComponents(parseProgram(file))) {
          if (entry.nodes.length >= 2) {
            components.push({
              file: `${scope.prefix}${relative(scope.root, file).split('\\').join('/')}`,
              component: entry.component,
              count: entry.nodes.length,
            })
          }
        }
      }
    }
    return components
  }

  it('registers exactly the components the tree renders two regions in', () => {
    const observed = new Set(
      multiRegionComponents().map(
        (entry) => `${entry.file}::${entry.component}`,
      ),
    )
    const declared = new Set(
      WRITE_FEEDBACK_REGION_EXEMPTIONS.map(
        (entry) => `${entry.file}::${entry.component}`,
      ),
    )
    const unregistered = [...observed].filter((key) => !declared.has(key)).sort()
    const stale = [...declared].filter((key) => !observed.has(key)).sort()
    expect(
      unregistered,
      'components rendering two regions without an exemption',
    ).toEqual([])
    expect(stale, 'exemptions no component needs any more').toEqual([])
  }, 60_000)

  it('states a reason on every exemption and names unique components', () => {
    const keys = new Set()
    for (const entry of WRITE_FEEDBACK_REGION_EXEMPTIONS) {
      expect(entry.reason.trim().length).toBeGreaterThan(0)
      const key = `${entry.file}::${entry.component}`
      expect(keys.has(key), `duplicate exemption ${key}`).toBe(false)
      keys.add(key)
    }
  })

  it("leaves FileUploader's per-row results alone", () => {
    const uploader = join(
      packagesRoot,
      'ui-kit/src/components/FileUploader.tsx',
    )
    expect(collectRegionComponents(parseProgram(uploader))).toEqual([])
  })
})
