const BANNED_MODULES = new Set(['axios', 'node-fetch'])

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
  const slash = source.indexOf('/')
  return slash === -1 ? source : source.slice(0, slash)
}

function isBannedModuleSource(module) {
  return BANNED_MODULES.has(packageName(module))
}

function checkBannedSource(context, sourceNode) {
  const module = importSourceValue(sourceNode)
  if (module !== undefined && isBannedModuleSource(module)) {
    context.report({
      node: sourceNode,
      messageId: 'bannedModule',
      data: { module },
    })
  }
}

// A name the host merely declares as a global has a scope variable with no
// definitions, and must not silence the guard.
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

const ENVIRONMENT_OBJECTS = new Set(['window', 'globalThis', 'self'])

function environmentFetchSource(node, sourceCode) {
  if (
    node.type !== 'MemberExpression' ||
    node.object.type !== 'Identifier' ||
    !ENVIRONMENT_OBJECTS.has(node.object.name) ||
    isBound(node.object.name, node, sourceCode)
  ) {
    return undefined
  }
  if (!node.computed) {
    return node.property.type === 'Identifier' &&
      node.property.name === 'fetch'
      ? node.object.name
      : undefined
  }
  return node.property.type === 'Literal' &&
    node.property.value === 'fetch'
    ? node.object.name
    : undefined
}

function environmentFetchPattern(pattern) {
  if (pattern.type !== 'ObjectPattern') {
    return undefined
  }
  const property = pattern.properties.find(
    (candidate) =>
      candidate.type === 'Property' &&
      candidate.key.type === 'Identifier' &&
      candidate.key.name === 'fetch',
  )
  return property === undefined ? undefined : property.key
}

export const noDirectHttpRule = {
  meta: {
    type: 'problem',
    docs: {
      description:
        'Disallow direct HTTP calls outside @speed/api-client; every request must go through the api-client request function.',
    },
    messages: {
      globalFetch:
        'A direct fetch() call is hand-written HTTP; route the request through the @speed/api-client request function (createClient) instead.',
      memberFetch:
        '{{object}}.fetch() bypasses the @speed/api-client request layer; route the request through the client instance instead.',
      capturedFetch:
        'Capturing {{object}}.fetch (or destructuring fetch out of {{object}}) takes the global fetch outside a call and outside @speed/api-client, the only package allowed to touch the network; route requests through its request function instead.',
      xmlHttpRequest:
        'XMLHttpRequest bypasses the @speed/api-client request layer; route the request through the client instance instead.',
      bannedModule:
        'Importing {{module}} writes HTTP outside @speed/api-client, the only package allowed to touch the network; route the request through its request function instead.',
    },
    schema: [],
  },
  create(context) {
    const sourceCode = context.sourceCode

    function report(node, messageId, data) {
      context.report({ node, messageId, data })
    }

    return {
      CallExpression(node) {
        const callee = node.callee
        if (
          callee.type === 'Identifier' &&
          callee.name === 'fetch' &&
          !isBound('fetch', callee, sourceCode)
        ) {
          report(callee, 'globalFetch')
          return
        }
        if (
          callee.type === 'Identifier' &&
          callee.name === 'require' &&
          node.arguments.length === 1 &&
          node.arguments[0] !== undefined
        ) {
          checkBannedSource(context, node.arguments[0])
          return
        }
        const object = environmentFetchSource(callee, sourceCode)
        if (object !== undefined) {
          report(callee, 'memberFetch', { object })
        }
      },
      // Skip callee positions: the call visitor reports them, so counting
      // them here would double-report.
      MemberExpression(node) {
        if (
          node.parent !== null &&
          node.parent.type === 'CallExpression' &&
          node.parent.callee === node
        ) {
          return
        }
        const object = environmentFetchSource(node, sourceCode)
        if (object !== undefined) {
          report(node, 'capturedFetch', { object })
        }
      },
      VariableDeclarator(node) {
        if (node.init === null || node.init === undefined) {
          return
        }
        if (
          node.init.type === 'Identifier' &&
          ENVIRONMENT_OBJECTS.has(node.init.name) &&
          !isBound(node.init.name, node.init, sourceCode)
        ) {
          const key = environmentFetchPattern(node.id)
          if (key !== undefined) {
            report(key, 'capturedFetch', { object: node.init.name })
          }
        }
      },
      AssignmentExpression(node) {
        if (node.right.type !== 'Identifier') {
          return
        }
        if (
          !ENVIRONMENT_OBJECTS.has(node.right.name) ||
          isBound(node.right.name, node.right, sourceCode)
        ) {
          return
        }
        const key = environmentFetchPattern(node.left)
        if (key !== undefined) {
          report(key, 'capturedFetch', { object: node.right.name })
        }
      },
      NewExpression(node) {
        const callee = node.callee
        if (
          callee.type === 'Identifier' &&
          callee.name === 'XMLHttpRequest' &&
          !isBound('XMLHttpRequest', callee, sourceCode)
        ) {
          report(callee, 'xmlHttpRequest')
        }
      },
      ImportDeclaration(node) {
        checkBannedSource(context, node.source)
      },
      ExportNamedDeclaration(node) {
        if (node.source !== null && node.source !== undefined) {
          checkBannedSource(context, node.source)
        }
      },
      ExportAllDeclaration(node) {
        checkBannedSource(context, node.source)
      },
      ImportExpression(node) {
        checkBannedSource(context, node.source)
      },
    }
  },
}
