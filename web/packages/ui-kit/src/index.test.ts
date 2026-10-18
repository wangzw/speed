/**
 * The entry's export surface, guarded two ways.
 *
 * The package's public surface is exactly what src/index.ts re-exports.
 * The first assertion is a mechanism: it walks the entry's own
 * export-from declarations, enumerates each resolved source module's
 * exported symbols with the TypeScript compiler API, and asserts the
 * entry re-exports every one of them. A public name declared in a module
 * the entry already exposes and forgotten in the entry fails here.
 *
 * The mechanism is deliberately one-directional: the module list derives
 * from the entry's own declarations, so deleting a whole export block --
 * or a whole module the entry does not expose -- is invisible to it. The
 * second assertion pins the entry's exported name list against a fixed
 * list, so a lost export of either kind (a value, or a type-only name
 * such as WriteOutcome) fails here too.
 */

import { readFileSync } from 'node:fs'
import { dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import ts from 'typescript'

const srcDir = resolve(dirname(fileURLToPath(import.meta.url)))

/** The entry's relative from-specifiers (`export { ... } from './x.js'`). */
function exportFromSpecifiers(source: ts.SourceFile): string[] {
  const specifiers: string[] = []
  for (const statement of source.statements) {
    if (!ts.isExportDeclaration(statement)) {
      continue
    }
    const specifier = statement.moduleSpecifier
    if (specifier !== undefined && ts.isStringLiteral(specifier)) {
      specifiers.push(specifier.text)
    }
  }
  return specifiers
}

/** All exported symbol names of one source file (values and types alike). */
function exportedNames(program: ts.Program, source: ts.SourceFile): string[] {
  const checker = program.getTypeChecker()
  const moduleSymbol = checker.getSymbolAtLocation(source)
  if (moduleSymbol === undefined) {
    return []
  }
  return checker
    .getExportsOfModule(moduleSymbol)
    .map((symbol) => symbol.name)
    .filter((name): name is string => name !== undefined)
}

const options: ts.CompilerOptions = {
  module: ts.ModuleKind.NodeNext,
  moduleResolution: ts.ModuleResolutionKind.NodeNext,
  target: ts.ScriptTarget.ES2022,
  skipLibCheck: true,
}

function createEntryProgram(): {
  readonly program: ts.Program
  readonly indexPath: string
  readonly entryFile: ts.SourceFile
  readonly modulePaths: readonly string[]
} {
  const indexPath = resolve(srcDir, 'index.ts')
  const entrySource = ts.createSourceFile(
    indexPath,
    readFileSync(indexPath, 'utf8'),
    ts.ScriptTarget.ES2022,
    /* setParentNodes */ true,
    ts.ScriptKind.TS,
  )
  const moduleHost: ts.ModuleResolutionHost = {
    fileExists: ts.sys.fileExists,
    readFile: ts.sys.readFile,
  }
  const modulePaths = exportFromSpecifiers(entrySource).map((specifier) => {
    const resolved = ts.resolveModuleName(
      specifier,
      indexPath,
      options,
      moduleHost,
    )
    return resolved.resolvedModule?.resolvedFileName
  })
  const program = ts.createProgram({
    rootNames: [
      indexPath,
      ...modulePaths.filter((path): path is string => path !== undefined),
    ],
    options,
  })
  const entryFile = program.getSourceFile(indexPath)
  expect(entryFile).not.toBeUndefined()
  return {
    program,
    indexPath,
    entryFile: entryFile!,
    modulePaths: modulePaths.filter(
      (path): path is string => path !== undefined,
    ),
  }
}

/** Every name a consumer can import from the package entry. */
const PUBLIC_EXPORTS = [
  'AppTheme',
  'AppThemeMode',
  'AppThemeModeControl',
  'AppThemeProvider',
  'AppThemeProviderProps',
  'AsyncSection',
  'AsyncSectionEmptyState',
  'AsyncSectionErrorState',
  'AsyncSectionProps',
  'AsyncSectionStaleState',
  'CONFIRM_ARM_LOCKOUT_MS',
  'ConfirmDialog',
  'ConfirmDialogProps',
  'ConfirmDialogVariant',
  'DataTable',
  'DataTableColumn',
  'DataTableColumnCellWrap',
  'DataTableColumnPriority',
  'DataTableFilter',
  'DataTablePagination',
  'DataTableProps',
  'DataTableSort',
  'DataTableSortDirection',
  'EmptyState',
  'EmptyStateHeadingLevel',
  'EmptyStateNoHeading',
  'EmptyStateProps',
  'EmptyStateVariant',
  'FileUploader',
  'FileUploaderProps',
  'FileUploaderRow',
  'FileUploaderRowStatus',
  'FormField',
  'FormFieldProps',
  'FormFieldRenderState',
  'FormLayout',
  'FormLayoutColumns',
  'FormLayoutProps',
  'INVALID_ERROR_KEY',
  'InlineError',
  'InlineErrorProps',
  'InlineSuccess',
  'InlineSuccessProps',
  'ListSkeleton',
  'ListSkeletonProps',
  'PRIMARY_COLUMN_MAX_WIDTH',
  'PageHeader',
  'PageHeaderBreadcrumb',
  'PageHeaderProps',
  'REQUIRED_ERROR_KEY',
  'SidePanel',
  'SidePanelProps',
  'SidePanelSize',
  'UI_KIT_NAMESPACE',
  'WriteFeedback',
  'WriteFeedbackRegion',
  'WriteFeedbackRegionProps',
  'WriteOutcome',
  'createAppTheme',
  'errorCodeOf',
  'primaryColumnMaxWidthSx',
  'touchTargetSx',
  'uiKitResources',
  'useAppThemeMode',
  'useWriteFeedback',
  'visuallyHiddenSx',
  'zodFormResolver',
] as const

describe('the entry export surface', () => {
  it('re-exports every public name its source modules declare', () => {
    const { program, entryFile, modulePaths } = createEntryProgram()
    expect(modulePaths.length).toBeGreaterThan(0)
    const entryNames = new Set(exportedNames(program, entryFile))
    const missingAcrossModules: string[] = []
    for (const modulePath of modulePaths) {
      const moduleFile = program.getSourceFile(modulePath)
      if (moduleFile === undefined) {
        continue
      }
      for (const name of exportedNames(program, moduleFile)) {
        if (!entryNames.has(name)) {
          missingAcrossModules.push(
            `${relative(srcDir, modulePath)}: ${name}`,
          )
        }
      }
    }
    expect(
      missingAcrossModules,
      'names a consumer cannot import from the package entry:',
    ).toEqual([])
    // Whole-tree type-checking under coverage instrumentation outruns vitest's 5s default.
  }, 30000)

  it('exports exactly the fixed public list, types included', () => {
    const { program, entryFile } = createEntryProgram()
    expect(exportedNames(program, entryFile).sort()).toEqual(
      [...PUBLIC_EXPORTS].sort(),
    )
  }, 30000)
})
