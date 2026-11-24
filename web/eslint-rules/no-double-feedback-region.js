import path from 'node:path'

const REGION_TAG_NAMES = new Set([
  'WriteFeedbackRegion',
  'SharedWriteFeedbackRegion',
])

export const WRITE_FEEDBACK_REGION_EXEMPTIONS = [
  {
    file: 'packages/admin-ui/src/AccountsPage.tsx',
    component: 'AccountsPage',
    reason:
      "The list result (row deletes and the create panel's confirmation) and the detail panel's action row, sessions section and identifiers section are separate control groups, each with its region under its own controls.",
  },
  {
    file: 'packages/admin-ui/src/AccountsPage.tsx',
    component: 'AccountDetailWrites',
    reason:
      'The status, MFA requirement, profile patch, temporary password, identifier add and secret create rows are six control groups, each with its own scope and its region under its own row.',
  },
  {
    file: 'packages/admin-ui/src/PermissionsPage.tsx',
    component: 'PermissionsPageBody',
    reason:
      'The roles, grants, bindings and lookup sections are four separate control groups, each with its region under its own table.',
  },
  {
    file: 'packages/admin-ui/src/SmsPage.tsx',
    component: 'SmsPage',
    reason:
      'The register form, the offer form and the templates table are separate control groups, each with its region under its own controls.',
  },
  {
    file: 'packages/admin-ui/src/SsoPage.tsx',
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
    file: 'packages/admin-ui/src/ImpersonationsPage.tsx',
    component: 'ImpersonationsPage',
    reason:
      "The start form and the records table with its row actions are separate control groups; the start form's region stays mounted when its form unmounts.",
  },
  {
    file: 'packages/admin-ui/src/InvitationsPage.tsx',
    component: 'InvitationsPage',
    reason:
      'The invitations table and the members table are separate control groups, each with its region under its own table.',
  },
  {
    file: 'packages/admin-ui/src/OrganizationMembersPage.tsx',
    component: 'OrganizationMembersPage',
    reason:
      'The members table and the invitations table are separate control groups, each with its region under its own table.',
  },
]

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
