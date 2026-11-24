const SEED_KINDS = new Map([
  ['page', 'page'],
  ['context', 'context'],
  ['request', 'request'],
])

const EVALUATE_METHODS = new Set([
  'evaluate',
  'evaluateHandle',
  'evaluateAll',
  '$eval',
  '$$eval',
  'addInitScript',
])

const ROUTE_METHODS = new Set([
  'route',
  'routeFromHAR',
  'routeWebSocket',
  'unroute',
  'unrouteAll',
])

const BANNED_NETWORK_PACKAGES = new Set([
  'axios',
  'node-fetch',
  'ws',
  'http',
  'https',
  'http2',
  'net',
])

const BANNED_ASSEMBLY_PACKAGES = new Set([
  'child_process',
  'better-sqlite3',
  'sqlite3',
  'sql.js',
  'node:sqlite',
  '@libsql/client',
  'knex',
  'sequelize',
  'typeorm',
  'prisma',
  'pg',
  'pg-promise',
  'mysql',
  'mysql2',
  'mongodb',
])

const FS_PACKAGES = new Set(['fs'])
const FS_WRITE_FUNCTIONS = new Set([
  'appendFile',
  'appendFileSync',
  'chmod',
  'chmodSync',
  'chown',
  'chownSync',
  'copyFile',
  'copyFileSync',
  'cp',
  'cpSync',
  'createWriteStream',
  'link',
  'linkSync',
  'mkdir',
  'mkdirSync',
  'mkdtemp',
  'mkdtempSync',
  'open',
  'openSync',
  'rename',
  'renameSync',
  'rm',
  'rmSync',
  'rmdir',
  'rmdirSync',
  'symlink',
  'symlinkSync',
  'truncate',
  'truncateSync',
  'unlink',
  'unlinkSync',
  'utimes',
  'utimesSync',
  'write',
  'writeFile',
  'writeFileSync',
  'writeSync',
])

const ALLOWED_LOG_EXPORTS = new Set(['waitForVerificationCode'])

const ENVIRONMENT_OBJECTS = new Set(['window', 'globalThis', 'self'])

const ALLOWED_CDP_DOMAIN = 'WebAuthn'

const CDP_COMMAND = /^[A-Za-z][A-Za-z0-9]*\./

const UNWRAP_TYPES = new Set([
  'AwaitExpression',
  'ChainExpression',
  'ParenthesizedExpression',
  'TSAsExpression',
  'TSNonNullExpression',
  'TSSatisfiesExpression',
  'TypeCastExpression',
])

function importSourceValue(source) {
  if (source.type === 'Literal' && typeof source.value === 'string') {
    return source.value
  }
  if (
    source.type === 'TemplateLiteral' &&
    source.expressions.length === 0 &&
    source.quasis.length === 1
  ) {
    const cooked = source.quasis[0].value.cooked
    return typeof cooked === 'string' ? cooked : undefined
  }
  return undefined
}

function packageName(source) {
  const normalized = source.startsWith('node:') ? source.slice(5) : source
  const slash = normalized.indexOf('/')
  return slash === -1 ? normalized : normalized.slice(0, slash)
}

function isBound(name, node, sourceCode) {
  for (
    let scope = sourceCode.getScope(node);
    scope !== null && scope !== undefined;
    scope = scope.upper
  ) {
    if (
      scope.variables.some(
        (variable) => variable.name === name && variable.defs.length > 0,
      )
    ) {
      return true
    }
  }
  return false
}

function memberName(member) {
  if (!member.computed) {
    return member.property.type === 'Identifier'
      ? member.property.name
      : undefined
  }
  return member.property.type === 'Literal' &&
    typeof member.property.value === 'string'
    ? member.property.value
    : undefined
}

function isEnvironmentFetch(callee, sourceCode) {
  if (callee.type !== 'MemberExpression') {
    return false
  }
  if (memberName(callee) !== 'fetch') {
    return false
  }
  const object = callee.object
  return (
    object.type === 'Identifier' &&
    ENVIRONMENT_OBJECTS.has(object.name) &&
    !isBound(object.name, callee, sourceCode)
  )
}

function unwrap(node) {
  let current = node
  while (current && UNWRAP_TYPES.has(current.type)) {
    current =
      current.type === 'AwaitExpression' ? current.argument : current.expression
  }
  return current
}

function isNewCdpSession(node) {
  return (
    node !== null &&
    node !== undefined &&
    node.type === 'CallExpression' &&
    node.callee.type === 'MemberExpression' &&
    memberName(node.callee) === 'newCDPSession'
  )
}

function importedNames(node) {
  const names = []
  let opaque = false
  let typeOnly = node.importKind === 'type'
  for (const specifier of node.specifiers) {
    if (specifier.type === 'ImportSpecifier') {
      if (specifier.importKind === 'type') typeOnly = true
      const imported = specifier.imported
      names.push(
        imported.type === 'Identifier' ? imported.name : String(imported.value),
      )
    } else {
      opaque = true
    }
  }
  return { names, opaque, typeOnly }
}

function isLogModule(source) {
  const normalized = source.startsWith('node:') ? source.slice(5) : source
  return /(^|\/)fixtures\/log$/.test(normalized) || /(^|\/)log$/.test(normalized)
}

export const noE2eBypassRule = {
  meta: {
    type: 'problem',
    docs: {
      description:
        'Disallow network, route-interception, evaluate, non-WebAuthn CDP and assembly-layer calls in the e2e tier; journeys interact only through rendered UI.',
    },
    messages: {
      directHttp:
        'Direct HTTP ({{module}}) bypasses the browser: an e2e journey must act through the rendered UI (clicks and keyboard), not by issuing requests.',
      evaluate:
        '{{name}}() executes script in the page: assert on rendered text, roles, focus and URL instead of evaluating script, and drive the UI with locators.',
      route:
        '{{name}}() fakes the backend: the e2e tier runs against the real server, so route interception and HAR/websocket replay are not allowed.',
      requestContext:
        'The Playwright API request context issues HTTP outside the browser: an e2e journey must interact through the page, not the request fixture.',
      cdp:
        'CDP command {{domain}} is outside the one allowed family: only WebAuthn.* virtual-authenticator setup may use a CDP session.',
      cdpDynamic:
        'A CDP send with a non-literal command cannot be proven to stay inside the WebAuthn.* virtual-authenticator family, so it is not allowed.',
      connectOverCdp:
        'connectOverCDP is a general CDP side channel: only a WebAuthn.* virtual-authenticator fixture may drive the browser over CDP.',
      assemblyOnly:
        'A spec or helper may not use {{what}}: the harness assembles its environment in fixtures/, and the journey itself must go through the rendered UI.',
      logInterpretation:
        'A spec or helper may not read raw process-log text: import waitForVerificationCode for a code, or ask the server fixture for the review carrier message or the outbound delivery record.',
    },
    schema: [
      {
        type: 'object',
        properties: {
          allowAssembly: { type: 'boolean' },
        },
        additionalProperties: false,
      },
    ],
  },
  create(context) {
    const sourceCode = context.sourceCode
    const options = context.options[0] ?? {}
    const allowAssembly = options.allowAssembly === true

    function report(node, messageId, data) {
      context.report({ node, messageId, data })
    }

    function children(node) {
      const keys =
        sourceCode.visitorKeys[node.type] ??
        Object.keys(node).filter(
          (key) =>
            !['parent', 'loc', 'range', 'start', 'end', 'tokens', 'comments'].includes(
              key,
            ),
        )
      const result = []
      for (const key of keys) {
        const value = node[key]
        if (Array.isArray(value)) {
          for (const item of value) {
            if (item && typeof item.type === 'string') result.push(item)
          }
        } else if (value && typeof value.type === 'string') {
          result.push(value)
        }
      }
      return result
    }

    function walk(node, visit) {
      visit(node)
      for (const child of children(node)) walk(child, visit)
    }

    const taint = new Map()

    function bindPattern(pattern, kind) {
      if (!pattern) return false
      let changed = false
      const set = (name) => {
        if (taint.get(name) !== kind) {
          taint.set(name, kind)
          changed = true
        }
      }
      if (pattern.type === 'Identifier') {
        if (kind === undefined) return false
        set(pattern.name)
      } else if (pattern.type === 'AssignmentPattern') {
        changed = bindPattern(pattern.left, kind) || changed
      } else if (pattern.type === 'ObjectPattern') {
        for (const property of pattern.properties) {
          if (property.type !== 'Property') continue
          const key =
            property.key.type === 'Identifier'
              ? property.key.name
              : property.key.type === 'Literal'
                ? String(property.key.value)
                : undefined
          if (key === undefined) continue
          const kindForProperty = SEED_KINDS.get(key) ?? kind
          if (kindForProperty === undefined) continue
          changed = bindPattern(property.value, kindForProperty) || changed
        }
      }
      return changed
    }

    function kindOf(node) {
      if (node === null || node === undefined) return undefined
      const core = unwrap(node)
      if (core.type === 'Identifier') {
        return taint.get(core.name)
      }
      if (core.type === 'MemberExpression' && memberName(core) === 'request') {
        const base = kindOf(core.object)
        if (base === 'page' || base === 'context') return 'request'
      }
      return undefined
    }

    function propagate(node) {
      let changed = false

      const visit = (current) => {
        if (
          current.type === 'FunctionDeclaration' ||
          current.type === 'FunctionExpression' ||
          current.type === 'ArrowFunctionExpression'
        ) {
          for (const parameter of current.params) {
            if (parameter.type === 'Identifier') {
              const seeded = SEED_KINDS.get(parameter.name)
              if (seeded !== undefined) {
                if (taint.get(parameter.name) !== seeded) {
                  taint.set(parameter.name, seeded)
                  changed = true
                }
              }
            } else if (parameter.type === 'ObjectPattern') {
              changed = bindPattern(parameter, undefined) || changed
            }
          }
        }

        if (current.type === 'VariableDeclarator') {
          const init = current.init ? unwrap(current.init) : undefined
          let kind
          if (init !== undefined) {
            if (isNewCdpSession(init)) {
              kind = 'cdp'
            } else {
              kind = kindOf(init)
            }
          }
          if (current.id.type === 'Identifier') {
            if (kind === undefined) {
              kind = SEED_KINDS.get(current.id.name)
            }
            if (kind !== undefined && taint.get(current.id.name) !== kind) {
              taint.set(current.id.name, kind)
              changed = true
            }
          } else if (current.id.type === 'ObjectPattern') {
            changed = bindPattern(current.id, kind) || changed
          }
        }

        if (
          current.type === 'AssignmentExpression' &&
          current.left.type === 'Identifier'
        ) {
          const kind = kindOf(current.right)
          if (kind !== undefined && taint.get(current.left.name) !== kind) {
            taint.set(current.left.name, kind)
            changed = true
          }
        }
      }

      walk(node, visit)
      return changed
    }

    function checkImportSpecifier(sourceNode, declared) {
      const module = importSourceValue(sourceNode)
      if (module === undefined) return
      const pkg = packageName(module)
      const normalized = module.startsWith('node:') ? module.slice(5) : module

      if (declared !== undefined && declared.typeOnly) return

      if (!allowAssembly && BANNED_NETWORK_PACKAGES.has(pkg)) {
        report(sourceNode, 'directHttp', { module })
        return
      }

      if (!allowAssembly && BANNED_ASSEMBLY_PACKAGES.has(pkg)) {
        report(sourceNode, 'assemblyOnly', { what: `\`${module}\`` })
        return
      }

      if (!allowAssembly && FS_PACKAGES.has(pkg)) {
        if (declared === undefined || declared.opaque) {
          report(sourceNode, 'assemblyOnly', {
            what: `\`${module}\` as a namespace or default binding`,
          })
          return
        }
        const writes = declared.names.filter((name) =>
          FS_WRITE_FUNCTIONS.has(name),
        )
        if (writes.length > 0) {
          report(sourceNode, 'assemblyOnly', {
            what: `filesystem write(s) ${writes.join(', ')}`,
          })
          return
        }
      }

      if (!allowAssembly && isLogModule(module)) {
        if (declared === undefined || declared.opaque) {
          report(sourceNode, 'logInterpretation')
          return
        }
        const forbidden = declared.names.filter(
          (name) =>
            !ALLOWED_LOG_EXPORTS.has(name) &&
            name !== 'VerificationKind' &&
            name !== 'VerificationCode',
        )
        if (forbidden.length > 0) {
          report(sourceNode, 'logInterpretation')
        }
      }
    }

    function checkImportDeclaration(node) {
      const declared = importedNames(node)
      checkImportSpecifier(node.source, declared)
    }

    function checkRequire(call) {
      if (call.arguments.length !== 1) return
      const module = importSourceValue(call.arguments[0])
      if (module === undefined) return
      const declared = { names: [], opaque: true, typeOnly: false }
      checkImportSpecifier(call.arguments[0], declared)
    }

    function checkSend(call, callee) {
      const command = importSourceValue(call.arguments[0])
      if (command === undefined) {
        if (kindOf(callee.object) === 'cdp') {
          report(callee.property, 'cdpDynamic')
        }
        return
      }
      if (!CDP_COMMAND.test(command)) {
        return
      }
      const domain = command.slice(0, command.indexOf('.'))
      if (domain === ALLOWED_CDP_DOMAIN) {
        return
      }
      report(callee.property, 'cdp', { domain })
    }

    function checkCall(node) {
      const callee = node.callee
      if (
        callee.type === 'Identifier' &&
        callee.name === 'fetch' &&
        !isBound('fetch', callee, sourceCode)
      ) {
        report(callee, 'directHttp', { module: 'fetch' })
        return
      }
      if (callee.type === 'Identifier' && callee.name === 'require') {
        checkRequire(node)
        return
      }
      if (isEnvironmentFetch(callee, sourceCode)) {
        report(callee.property, 'directHttp', { module: 'fetch' })
        return
      }
      if (callee.type !== 'MemberExpression') return

      const name = memberName(callee)
      if (name === undefined) return

      if (EVALUATE_METHODS.has(name)) {
        report(callee.property, 'evaluate', { name })
        return
      }
      if (ROUTE_METHODS.has(name)) {
        report(callee.property, 'route', { name })
        return
      }
      if (name === 'connectOverCDP') {
        report(callee.property, 'connectOverCdp')
        return
      }
      if (name === 'send') {
        checkSend(node, callee)
        return
      }
      if (
        kindOf(callee.object) === 'request' ||
        (callee.object.type === 'MemberExpression' &&
          memberName(callee.object) === 'request')
      ) {
        report(callee.property, 'requestContext')
      }
    }

    function checkMember(node) {
      // A member in callee position is reported by checkCall, and one in the
      // middle of a longer chain at the chain's end; reporting here would
      // double-report.
      if (
        node.parent !== null &&
        node.parent !== undefined &&
        ((node.parent.type === 'MemberExpression' && node.parent.object === node) ||
          (node.parent.type === 'CallExpression' && node.parent.callee === node))
      ) {
        return
      }
      if (kindOf(node) === 'request') {
        report(node.property, 'requestContext')
        return
      }
      const name = memberName(node)
      if (name === 'fetch') {
        const object = node.object
        if (
          object.type === 'Identifier' &&
          ENVIRONMENT_OBJECTS.has(object.name) &&
          !isBound(object.name, node, sourceCode)
        ) {
          report(node.property, 'directHttp', { module: 'fetch' })
        }
      }
      if (!allowAssembly && (name === 'text' || name === 'lines')) {
        const object = node.object
        if (object.type === 'MemberExpression' && memberName(object) === 'log') {
          report(node.property, 'logInterpretation')
        }
      }
    }

    function checkNew(node) {
      const callee = node.callee
      if (
        callee.type === 'Identifier' &&
        callee.name === 'XMLHttpRequest' &&
        !isBound('XMLHttpRequest', callee, sourceCode)
      ) {
        report(callee, 'directHttp', { module: 'XMLHttpRequest' })
        return
      }
      if (
        callee.type === 'Identifier' &&
        callee.name === 'WebSocket' &&
        !isBound('WebSocket', callee, sourceCode)
      ) {
        report(callee, 'directHttp', { module: 'WebSocket' })
        return
      }
      if (isEnvironmentFetch(callee, sourceCode)) {
        report(callee.property, 'directHttp', { module: 'fetch' })
      }
    }

    return {
      Program(node) {
        for (let round = 0; round < 16; round += 1) {
          if (!propagate(node)) break
        }

        walk(node, (current) => {
          switch (current.type) {
            case 'ImportDeclaration':
              checkImportDeclaration(current)
              break
            case 'ExportNamedDeclaration':
              if (current.source) {
                checkImportSpecifier(current.source, {
                  names: [],
                  opaque: true,
                  typeOnly: false,
                })
              }
              break
            case 'ExportAllDeclaration':
              if (current.source) {
                checkImportSpecifier(current.source, {
                  names: [],
                  opaque: true,
                  typeOnly: false,
                })
              }
              break
            case 'ImportExpression':
              checkImportSpecifier(current.source, {
                names: [],
                opaque: true,
                typeOnly: false,
              })
              break
            case 'CallExpression':
              checkCall(current)
              break
            case 'NewExpression':
              checkNew(current)
              break
            case 'MemberExpression':
              checkMember(current)
              break
            default:
              break
          }
        })
      },
    }
  },
}
