import { RuleTester } from 'eslint'
import { describe, expect, it } from 'vitest'
import { noDirectHttpRule } from './no-direct-http.js'

const ruleTester = new RuleTester({
  languageOptions: {
    parserOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
    },
  },
})

function runRule(valid, invalid) {
  try {
    ruleTester.run('no-direct-http', noDirectHttpRule, { valid, invalid })
  } catch (error) {
    expect.fail(`rule tests failed: ${error.message}`)
  }
}

describe('no-direct-http rule', () => {
  it('accepts calls that are not the global fetch', () => {
    runRule(
      [
        "const data = await api('/notes');",
        "const data = await client.get('/notes');",
        "const data = await sdk.notes.list({ page: 1 });",
        "fetchData('/api/notes');",
        "const response = myFetch('/api');",
        "window.postMessage({ kind: 'reload' }, '*');",
        "store.fetch('/api');",
        "const response = await fetcher.fetch(url);",
        "const { readFile } = require('fs');",
        "const format = require('./format.js');",
        "import { createClient } from '@speed/api-client';",
        "const mod = await import('./feature.js');",
      ],
      [],
    )
  })

  it('accepts a fetch identifier that resolves to a binding', () => {
    runRule(
      [
        'function getData(fetch) { return fetch("/api"); }',
        'const fetch = (url) => Promise.resolve(url); fetch("/api");',
        "import { fetch } from './local-http.js'; fetch('/api');",
        'function outer() { const fetch = () => {}; return function inner() { return fetch("/api"); }; }',
        'fetch("/api"); function fetch() {}',
        'class XMLHttpRequest {} new XMLHttpRequest();',
      ],
      [],
    )
  })

  it('flags bare global fetch calls', () => {
    runRule(
      [],
      [
        { code: 'fetch("/api/notes");', errors: [{ messageId: 'globalFetch' }] },
        {
          code: 'const data = await fetch("/api/notes", { method: "POST" });',
          errors: [{ messageId: 'globalFetch' }],
        },
        {
          code: 'async function load() { return fetch(url); }',
          errors: [{ messageId: 'globalFetch' }],
        },
      ],
    )
  })

  it('flags the environment fetch even when the host merely declares it a global', () => {
    runRule(
      [],
      [
        {
          code: '/* global fetch */\nfetch("/api/notes");',
          errors: [{ messageId: 'globalFetch' }],
        },
        {
          code: '/* global fetch, XMLHttpRequest */\nnew XMLHttpRequest();',
          errors: [{ messageId: 'xmlHttpRequest' }],
        },
      ],
    )
    const configuredTester = new RuleTester({
      languageOptions: {
        parserOptions: { ecmaVersion: 2022, sourceType: 'module' },
        globals: { fetch: 'readonly', XMLHttpRequest: 'readonly' },
      },
    })
    try {
      configuredTester.run('no-direct-http', noDirectHttpRule, {
        valid: [],
        invalid: [
          { code: 'fetch("/api");', errors: [{ messageId: 'globalFetch' }] },
          {
            code: 'window.fetch("/api");',
            errors: [{ messageId: 'memberFetch' }],
          },
          {
            code: 'new XMLHttpRequest();',
            errors: [{ messageId: 'xmlHttpRequest' }],
          },
        ],
      })
    } catch (error) {
      expect.fail(`rule tests failed: ${error.message}`)
    }
  })

  it('flags fetch through the environment object', () => {
    runRule(
      [],
      [
        {
          code: 'window.fetch("/api/notes");',
          errors: [{ messageId: 'memberFetch' }],
        },
        {
          code: 'const data = await globalThis.fetch("/api");',
          errors: [{ messageId: 'memberFetch' }],
        },
        {
          code: 'globalThis.fetch("/api"); window.fetch("/api");',
          errors: [
            { messageId: 'memberFetch' },
            { messageId: 'memberFetch' },
          ],
        },
        {
          code: 'window["fetch"]("/api");',
          errors: [{ messageId: 'memberFetch' }],
        },
        {
          code: 'const data = await self.fetch("/api");',
          errors: [{ messageId: 'memberFetch' }],
        },
        {
          code: 'globalThis["fetch"]("/api"); self["fetch"]("/api");',
          errors: [
            { messageId: 'memberFetch' },
            { messageId: 'memberFetch' },
          ],
        },
      ],
    )
  })

  it('honours shadowing of the environment object name in member forms', () => {
    runRule(
      [
        'function f(self) { return self.fetch("/api"); }',
        'const window = { fetch: (url) => url }; window.fetch("/api");',
        'function g(globalThis) { return globalThis.fetch("/api"); }',
        'function m(window) { const f = window.fetch.bind(window); return f; }',
        'function h(window) { const { fetch } = window; }',
        'function k(self) { ({ fetch } = self); }',
      ],
      [
        {
          code: 'window.fetch("/api/notes");',
          errors: [{ messageId: 'memberFetch' }],
        },
        {
          code: 'self.fetch("/api/notes");',
          errors: [{ messageId: 'memberFetch' }],
        },
        {
          code: 'const f = window.fetch;',
          errors: [{ messageId: 'capturedFetch' }],
        },
      ],
    )
  })

  it('flags the environment fetch captured out of a call: aliasing, references and destructuring', () => {
    runRule(
      [],
      [
        {
          code: 'const f = window.fetch;',
          errors: [{ messageId: 'capturedFetch' }],
        },
        {
          code: 'const f = globalThis["fetch"];',
          errors: [{ messageId: 'capturedFetch' }],
        },
        {
          code: 'const f = window.fetch.bind(window);',
          errors: [{ messageId: 'capturedFetch' }],
        },
        {
          code: 'self.fetch.call(undefined, "/api");',
          errors: [{ messageId: 'capturedFetch' }],
        },
        {
          code: 'const { fetch } = window; fetch("/api");',
          errors: [{ messageId: 'capturedFetch' }],
        },
        {
          code: 'const { fetch: g } = globalThis;',
          errors: [{ messageId: 'capturedFetch' }],
        },
        {
          code: 'const { fetch = fallback } = self;',
          errors: [{ messageId: 'capturedFetch' }],
        },
      ],
    )
  })

  it('flags constructed XMLHttpRequest', () => {
    runRule(
      [],
      [
        {
          code: 'new XMLHttpRequest();',
          errors: [{ messageId: 'xmlHttpRequest' }],
        },
        {
          code: 'const xhr = new XMLHttpRequest(); xhr.open("GET", "/api");',
          errors: [{ messageId: 'xmlHttpRequest' }],
        },
      ],
    )
  })

  it('flags axios and node-fetch imports, requires and dynamic imports', () => {
    runRule(
      [],
      [
        {
          code: "import axios from 'axios';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "import { post } from 'axios';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "import fetch from 'node-fetch';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "const axios = require('axios');",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "const { default: fetch } = require('node-fetch');",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "const mod = await import('axios');",
          errors: [{ messageId: 'bannedModule' }],
        },
      ],
    )
  })

  it('flags banned modules through re-exports, subpaths and template sources', () => {
    runRule(
      [
        "export { format } from './format.js';",
        "export * from './local-http.js';",
        "export * as fetch from './local-http.js';",
        "import adapter from 'axios-mock-adapter';",
        "import fromAxios from './axios.js';",
        "const mod = await import('./feature.js');",
        "const { readFile } = require('fs');",
      ],
      [
        {
          code: "export * from 'node-fetch';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "export * as fetch from 'node-fetch';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "export { default as axios } from 'axios';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "export { fetch } from 'node-fetch';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "import axios from 'axios/dist/node/axios.cjs';",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "const fetch = require('node-fetch/lib/index.js');",
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: 'const mod = await import(`axios`);',
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: 'const axios = require(`axios`);',
          errors: [{ messageId: 'bannedModule' }],
        },
        {
          code: "export { default as axios } from 'axios/dist/axios.cjs';",
          errors: [{ messageId: 'bannedModule' }],
        },
      ],
    )
  })

  it('reports each violation in mixed code, once per occurrence', () => {
    runRule(
      [],
      [
        {
          code: "import axios from 'axios'; fetch('/api'); window.fetch('/api');",
          errors: [
            { messageId: 'bannedModule' },
            { messageId: 'globalFetch' },
            { messageId: 'memberFetch' },
          ],
        },
      ],
    )
  })
})
