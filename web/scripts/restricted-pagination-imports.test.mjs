/**
 * Structural guard: the table pager belongs to ui-kit.
 *
 * MUI's pagination actions move focus off a direction the moment it becomes
 * unavailable (natively disabled or aria-disabled), and drop it to the
 * document body when both directions are unavailable. TablePager replaces
 * those actions; every other runtime source reaches the footer through
 * TablePager or a DataTable `pagination` prop. Both ESLint surfaces therefore
 * enable `no-restricted-imports` over their runtime src and exempt exactly one
 * source, `packages/ui-kit/src/components/TablePager.tsx`.
 *
 * The cases below load the real configs through ESLint, at real file paths
 * under each config's cwd, so they check the shipped configuration rather than
 * a copy of its rule table. A config that stops enabling the rule, widens the
 * exemption to the rest of ui-kit, or lets the exception reach the demo host
 * is red here.
 *
 * The last case scans the existing sources in both runtime scopes and expects
 * no hit. It is the TDD evidence for the change that moves the consumers onto
 * TablePager: before that lands, the accounts page and DataTable still import
 * MUI's pager, and this case fails naming them.
 */
import { ESLint } from 'eslint'
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const demoWebRoot = join(webRoot, '..', 'starters', 'demo', 'web')

/** The core rule both configs enable; the guard reads nothing else. */
const RULE_ID = 'no-restricted-imports'

/** The workspace config, anchored at web/ as `pnpm -C web lint` runs it. */
const webEslint = new ESLint({ cwd: webRoot })

/** The demo host's config, which imports the web config wholesale. */
const demoEslint = new ESLint({ cwd: demoWebRoot })

/**
 * The same two configs with only the guarded rule enabled at run time. The
 * workspace scan walks every source in both scopes, so running the other
 * rules there would repeat the lint leg's work; the config, its file scopes
 * and the rule's resolved options are still the shipped ones.
 */
function scanInstance(cwd) {
  return new ESLint({
    cwd,
    ruleFilter: ({ ruleId }) => ruleId === RULE_ID,
  })
}

/** A path under the web workspace. */
function webPath(...segments) {
  return join(webRoot, ...segments)
}

/** A path under the demo host. */
function demoPath(...segments) {
  return join(demoWebRoot, ...segments)
}

/** The restricted-import reports one source draws at one file path. */
async function restrictedMessages(eslint, filePath, code) {
  const [result] = await eslint.lintText(code, { filePath })
  return result.messages.filter((message) => message.ruleId === RULE_ID)
}

/** A default import of `@pager` written at the top of a source. */
function defaultImport(module) {
  return `import Pager from '${module}'\nexport default Pager\n`
}

describe('restricted pagination imports', () => {
  it.each([
    '@mui/material/TablePagination',
    '@mui/material/TablePaginationActions',
    '@mui/material/Pagination',
  ])('rejects a default import of %s in a package', async (module) => {
    const messages = await restrictedMessages(
      webEslint,
      webPath('packages', 'admin-ui', 'src', 'AccountsPage.tsx'),
      defaultImport(module),
    )
    expect(messages).toHaveLength(1)
    expect(messages[0].severity).toBe(2)
    expect(messages[0].message).toContain('TablePager')
  })

  it('rejects the pager names from @mui/material and allows other names', async () => {
    const filePath = webPath('packages', 'admin-ui', 'src', 'AccountsPage.tsx')
    const restricted = await restrictedMessages(
      webEslint,
      filePath,
      "import { TablePagination, TablePaginationActions, Pagination, Button } from '@mui/material'\n"
        + 'export const all = [TablePagination, TablePaginationActions, Pagination, Button]\n',
    )
    expect(restricted).toHaveLength(3)
    for (const message of restricted) {
      expect(message.severity).toBe(2)
      expect(message.message).toContain('TablePager')
    }
    const allowed = await restrictedMessages(
      webEslint,
      filePath,
      "import { Button } from '@mui/material'\nexport default Button\n",
    )
    expect(allowed).toEqual([])
  })

  it.each([
    '@mui/material/TablePagination/TablePagination',
    '@mui/material/TablePaginationActions/TablePaginationActions',
    '@mui/material/Pagination/Pagination',
  ])('rejects a deep import of %s in a package', async (module) => {
    const messages = await restrictedMessages(
      webEslint,
      webPath('packages', 'admin-ui', 'src', 'AccountsPage.tsx'),
      defaultImport(module),
    )
    expect(messages).toHaveLength(1)
    expect(messages[0].severity).toBe(2)
  })

  it('exempts only the TablePager source, not the rest of ui-kit', async () => {
    const pagerSource = await restrictedMessages(
      webEslint,
      webPath('packages', 'ui-kit', 'src', 'components', 'TablePager.tsx'),
      defaultImport('@mui/material/TablePagination'),
    )
    expect(pagerSource).toEqual([])
    const dataTable = await restrictedMessages(
      webEslint,
      webPath('packages', 'ui-kit', 'src', 'components', 'DataTable.tsx'),
      defaultImport('@mui/material/TablePagination'),
    )
    expect(dataTable).toHaveLength(1)
    expect(dataTable[0].severity).toBe(2)
  })

  it('rejects a pager import in the demo host', async () => {
    const messages = await restrictedMessages(
      demoEslint,
      demoPath('src', 'pages', 'x.tsx'),
      defaultImport('@mui/material/TablePagination'),
    )
    expect(messages).toHaveLength(1)
    expect(messages[0].severity).toBe(2)
    expect(messages[0].message).toContain('TablePager')
  })

  it('leaves test sources outside the runtime scope', async () => {
    for (const filePath of [
      webPath('packages', 'admin-ui', 'src', 'AccountsPage.test.tsx'),
      webPath('packages', 'admin-ui', 'src', 'AccountsPage.browser.test.tsx'),
      webPath('packages', 'admin-ui', 'test-utils', 'accounts-page.ts'),
    ]) {
      expect(
        await restrictedMessages(
          webEslint,
          filePath,
          defaultImport('@mui/material/TablePagination'),
        ),
        filePath,
      ).toEqual([])
    }
    expect(
      await restrictedMessages(
        demoEslint,
        demoPath('src', 'pages', 'x.test.tsx'),
        defaultImport('@mui/material/TablePagination'),
      ),
    ).toEqual([])
  })

  it('finds no pager import in either runtime scope', async () => {
    const results = [
      ...(await scanInstance(webRoot).lintFiles(['packages'])),
      ...(await scanInstance(demoWebRoot).lintFiles(['src'])),
    ]
    expect(results.length).toBeGreaterThan(0)
    const offenders = []
    for (const result of results) {
      const root = result.filePath.startsWith(demoWebRoot) ? demoWebRoot : webRoot
      for (const message of result.messages) {
        if (message.ruleId === RULE_ID) {
          const display = relative(root, result.filePath).split(sep).join('/')
          offenders.push(`${display}:${message.line}: ${message.message}`)
        }
      }
    }
    expect(
      offenders,
      'import the footer through TablePager or a DataTable pagination prop, '
        + 'so a direction that becomes unavailable keeps its focus',
    ).toEqual([])
  })
})
