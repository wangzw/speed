/**
 * no-double-feedback-region: one component renders one feedback region.
 *
 * A feedback scope is one `useWriteFeedback` instance plus the
 * `WriteFeedbackRegion` that renders its slot. A control group is the set
 * of write controls that share one result location, and it reports through
 * exactly one scope and one region under its controls.
 * When one component gives that group two scopes and two regions, a
 * refusal and a confirmation can stand on screen at the same time and
 * read as one another's result -- the defect the single-slot contract
 * exists to prevent. This rule makes the shape executable: a component
 * that renders two `WriteFeedbackRegion` elements is reported unless it
 * is registered below with a reason.
 *
 * The count is per declaring component, not per file: a page component may
 * own several control groups (a list, a detail panel, two titled sections)
 * and render one region for each. Those components are the entries in
 * `WRITE_FEEDBACK_REGION_EXEMPTIONS`; each reason states which separate
 * control groups the regions serve. The workspace scan in the rule's test
 * keeps the list exactly equal to the set of multi-region components under
 * each package's `src` and the demo host's `src` (the scopes the two lint
 * configs apply the rule to: the host is part of the product, so its own
 * config turns the rule on over the same shared implementation and the same
 * list), so a new one must be registered deliberately and a stale entry is
 * removed.
 *
 * A queue of independent per-row transfers is the design's allowed
 * exception (an upload row is its own slot), and it does not trip this
 * rule: `FileUploader` renders each row's status as text from its `rows`
 * props and never renders `WriteFeedbackRegion`, and a region rendered
 * once inside a `rows.map(...)` is one JSX element, not one per row.
 *
 * The rule judges the JSX tag name, including an aliased import of
 * `WriteFeedbackRegion` or the packages' `SharedWriteFeedbackRegion`
 * wrapper. Dynamic rendering the rule cannot see -- building an element
 * through `createElement` -- stays code review's residue.
 *
 * Scope is applied in eslint.config.mjs over package runtime source; test
 * files and test utilities are excluded there, not by the rule.
 */

import path from 'node:path'

/** The JSX tag names a feedback region renders under. */
const REGION_TAG_NAMES = new Set([
  'WriteFeedbackRegion',
  'SharedWriteFeedbackRegion',
])

/**
 * Components that legitimately render more than one region. Keyed by the
 * file (a package source as `packages/...`, a demo host source as
 * `starters/demo/web/src/...`) and the component's declaration name; the
 * reason states which separate control groups the regions serve. The scan
 * test in no-double-feedback-region.test.mjs holds this list to both trees.
 */
export const WRITE_FEEDBACK_REGION_EXEMPTIONS = [
  {
    file: 'packages/console-ui/src/AccountsPage.tsx',
    component: 'AccountsPage',
    reason:
      "The list result (row deletes and the create panel's confirmation) and the detail panel's action row, sessions section and identifiers section are separate control groups, each with its region under its own controls.",
  },
  {
    file: 'packages/console-ui/src/AccountsPage.tsx',
    component: 'AccountDetailWrites',
    reason:
      'The status, MFA requirement, profile patch, temporary password, identifier add and secret create rows are six control groups, each with its own scope and its region under its own row.',
  },
  {
    file: 'packages/console-ui/src/PermissionsPage.tsx',
    component: 'PermissionsPage',
    reason:
      'The roles, grants, bindings and lookup sections are four separate control groups, each with its region under its own table.',
  },
  {
    file: 'packages/console-ui/src/SmsPage.tsx',
    component: 'SmsPage',
    reason:
      'The register form, the offer form and the templates table are separate control groups, each with its region under its own controls.',
  },
  {
    file: 'packages/console-ui/src/SsoPage.tsx',
    component: 'SsoPage',
    reason:
      'The providers list, the provider write form and the domains section are separate control groups, each with its region under its own controls.',
  },
  {
    file: 'packages/account-ui/src/PreferencesSection.tsx',
    component: 'PreferencesSection',
    reason:
      'The language row and the time-zone row are separate control groups, each with its own scope and region.',
  },
  {
    file: 'packages/console-ui/src/ImpersonationsPage.tsx',
    component: 'ImpersonationsPage',
    reason:
      "The start form and the records table with its row actions are separate control groups; the start form's region stays mounted when its form unmounts.",
  },
  {
    file: 'packages/console-ui/src/InvitationsPage.tsx',
    component: 'InvitationsPage',
    reason:
      'The invitations table and the members table are separate control groups, each with its region under its own table.',
  },
]

/** The path in a message, relative to the repository for a host source
 * and to `web/` for a package source, whichever directory the lint run
 * started in. A path handed in already relative to its root stays as it
 * is. */
export function displayFile(filename) {
  const normalized = String(filename)
    .split(path.sep)
    .join('/')
    .replaceAll('\\', '/')
  if (
    normalized.startsWith('packages/') ||
    normalized.startsWith('starters/')
  ) {
    return normalized
  }
  // A starter is a program assembled from the packages, not a member of
  // the package library; its sources are keyed from the repository root
  // so a host file cannot collide with a package-relative one.
  const startersIndex = normalized.lastIndexOf('/starters/')
  if (startersIndex !== -1) {
    return normalized.slice(startersIndex + 1)
  }
  const webIndex = normalized.lastIndexOf('/web/')
  if (webIndex !== -1) {
    return normalized.slice(webIndex + '/web/'.length)
  }
  const packagesIndex = normalized.lastIndexOf('/packages/')
  if (packagesIndex !== -1) {
    return normalized.slice(packagesIndex + 1)
  }
  return normalized
}

/** The declaration name of the component a JSX element renders in: the
 * nearest enclosing named function, skipping anonymous callbacks (a
 * render prop belongs to the component that declares it). The ancestors
 * are the nodes enclosing the JSX element, outermost first. */
function componentNameOf(ancestors) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const current = ancestors[index]
    if (
      current.type !== 'FunctionDeclaration' &&
      current.type !== 'FunctionExpression' &&
      current.type !== 'ArrowFunctionExpression'
    ) {
      continue
    }
    if (current.type === 'FunctionDeclaration' && current.id !== null) {
      return current.id.name
    }
    const parent = index > 0 ? ancestors[index - 1] : undefined
    if (
      parent !== undefined &&
      parent.type === 'VariableDeclarator' &&
      parent.id !== null &&
      parent.id.type === 'Identifier'
    ) {
      return parent.id.name
    }
    if (
      parent !== undefined &&
      (parent.type === 'Property' || parent.type === 'MethodDefinition') &&
      parent.key !== null &&
      parent.key.type === 'Identifier'
    ) {
      return parent.key.name
    }
  }
  return '<component>'
}

/** Depth-first walk that skips the parent links ESLint adds and carries
 * the enclosing nodes, so the same collector runs on a parser's raw AST
 * and on ESLint's parent-linked one. */
function walk(node, ancestors, visit) {
  visit(node, ancestors)
  const nested = [...ancestors, node]
  for (const key of Object.keys(node)) {
    if (key === 'parent') {
      continue
    }
    const value = node[key]
    if (Array.isArray(value)) {
      for (const child of value) {
        if (child !== null && typeof child === 'object' && typeof child.type === 'string') {
          walk(child, nested, visit)
        }
      }
    } else if (
      value !== null &&
      typeof value === 'object' &&
      typeof value.type === 'string'
    ) {
      walk(value, nested, visit)
    }
  }
}

/** The JSX names that render a region in this file: the two canonical
 * names plus any local alias the imports bind. */
function regionNamesOf(program) {
  const names = new Set(REGION_TAG_NAMES)
  for (const statement of program.body) {
    if (statement.type !== 'ImportDeclaration') {
      continue
    }
    for (const specifier of statement.specifiers) {
      if (specifier.type !== 'ImportSpecifier') {
        continue
      }
      const imported = specifier.imported
      const importedName =
        imported.type === 'Identifier' ? imported.name : imported.value
      if (REGION_TAG_NAMES.has(importedName)) {
        names.add(specifier.local.name)
      }
    }
  }
  return names
}

/** Every component that renders at least one region, in source order of
 * its first region, with the region nodes it renders. */
export function collectRegionComponents(program) {
  const regionNames = regionNamesOf(program)
  const byComponent = new Map()
  walk(program, [], (node, ancestors) => {
    const isElement =
      node.type === 'JSXOpeningElement' || node.type === 'JSXSelfClosingElement'
    if (
      !isElement ||
      node.name === null ||
      node.name === undefined ||
      node.name.type !== 'JSXIdentifier' ||
      !regionNames.has(node.name.name)
    ) {
      return
    }
    const component = componentNameOf(ancestors)
    const entry = byComponent.get(component) ?? { component, nodes: [] }
    entry.nodes.push(node)
    byComponent.set(component, entry)
  })
  return [...byComponent.values()]
}

/** Whether this component is registered as a legitimate multi-region one. */
function isExempt(file, component) {
  return WRITE_FEEDBACK_REGION_EXEMPTIONS.some(
    (entry) => entry.file === file && entry.component === component,
  )
}

export const noDoubleFeedbackRegionRule = {
  meta: {
    type: 'problem',
    docs: {
      description:
        'Disallow rendering more than one WriteFeedbackRegion in one component; one feedback scope renders one region for its control group.',
    },
    messages: {
      doubleRegion:
        '{{file}} renders {{count}} WriteFeedbackRegion elements in component {{component}}. One feedback scope renders one region for its control group, so two regions can show two results at once. Make the groups share one scope, move the extra group into its own component, or register this component in WRITE_FEEDBACK_REGION_EXEMPTIONS with a reason.',
    },
    schema: [],
  },
  create(context) {
    const file = displayFile(context.filename)
    return {
      'Program:exit'(program) {
        for (const entry of collectRegionComponents(program)) {
          if (entry.nodes.length < 2 || isExempt(file, entry.component)) {
            continue
          }
          context.report({
            node: entry.nodes[1],
            messageId: 'doubleRegion',
            data: {
              file,
              component: entry.component,
              count: entry.nodes.length,
            },
          })
        }
      },
    }
  },
}
