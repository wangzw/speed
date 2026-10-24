/**
 * Rule tests for no-e2e-bypass. Run from the web workspace root:
 *
 *   pnpm exec vitest run eslint-rules/no-e2e-bypass.test.mjs
 *
 * The vitest CLI filter keeps this single-file run inside the plain node
 * environment; the packages' own tests are not picked up.
 *
 * Test snippets are fixture code for the rule under test, not rendered product
 * text, so the CJK scan does not apply to them.
 */

import { RuleTester } from 'eslint'
import { describe, expect, it } from 'vitest'
import { noE2eBypassRule } from './no-e2e-bypass.js'

const ruleTester = new RuleTester({
  languageOptions: {
    parserOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
    },
  },
})

/** Surface RuleTester's aggregate failure as the test failure. */
function runRule(valid, invalid) {
  try {
    ruleTester.run('no-e2e-bypass', noE2eBypassRule, { valid, invalid })
  } catch (error) {
    expect.fail(`rule tests failed: ${error.message}`)
  }
}

describe('no-e2e-bypass rule', () => {
  it('accepts rendered interaction through locators and the keyboard', () => {
    runRule(
      [
        "await page.getByLabel('Email').fill('operator@demo.test');",
        "await page.getByRole('button', { name: 'Sign in' }).click();",
        "await page.keyboard.press('Enter');",
        "await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();",
        "await page.getByText('Create an account').click();",
        // A shadowed fetch is a local, not the environment global.
        "function local(fetch) { return fetch('/x'); }",
        // Symbols the rule does not own are untouched.
        "const value = await page.getByTestId('count').textContent();",
      ],
      [],
    )
  })

  it('allows the one CDP family: WebAuthn virtual-authenticator setup', () => {
    runRule(
      [
        // The session handle the WebAuthn fixture needs.
        'const session = await context.newCDPSession(page);',
        // Every command inside the WebAuthn family is valid.
        "await session.send('WebAuthn.enable', { enableUI: true });",
        "await session.send('WebAuthn.addVirtualAuthenticator', { options: {} });",
        "await session.send('WebAuthn.addCredential', { credential: {} });",
        "await session.send('WebAuthn.setUserVerified');",
        // A non-CDP send (no dot in the command) is not a CDP command.
        "ws.send('hello');",
      ],
      [],
    )
  })

  it('flags the evaluate family', () => {
    runRule(
      [],
      [
        {
          code: 'await page.evaluate(() => document.body.innerHTML);',
          errors: [{ messageId: 'evaluate' }],
        },
        {
          code: "await page.evaluateHandle('1 + 1');",
          errors: [{ messageId: 'evaluate' }],
        },
        {
          code: "const text = await page.$eval('#x', (el) => el.textContent);",
          errors: [{ messageId: 'evaluate' }],
        },
        {
          code: "const all = await page.$$eval('.x', (els) => els.length);",
          errors: [{ messageId: 'evaluate' }],
        },
        {
          code: "await page.addInitScript(() => { window.x = 1 });",
          errors: [{ messageId: 'evaluate' }],
        },
        {
          // Computed member access is the same method.
          code: 'await page[\'evaluate\'](() => 1);',
          errors: [{ messageId: 'evaluate' }],
        },
      ],
    )
  })

  it('flags route interception and HAR/websocket replay', () => {
    runRule(
      [],
      [
        {
          code: "await page.route('**/*', (route) => route.abort());",
          errors: [{ messageId: 'route' }],
        },
        {
          code: "await context.routeFromHAR('x.har');",
          errors: [{ messageId: 'route' }],
        },
        {
          code: "await page.routeWebSocket('**/ws', (ws) => ws);",
          errors: [{ messageId: 'route' }],
        },
        {
          code: "await page.unroute('**/*');",
          errors: [{ messageId: 'route' }],
        },
        {
          code: 'await context.unrouteAll();',
          errors: [{ messageId: 'route' }],
        },
      ],
    )
  })

  it('flags the API request context, including destructured aliases', () => {
    runRule(
      [],
      [
        {
          code: "async ({ page }) => { await page.request.get('/api/v1/auth/me') }",
          errors: [{ messageId: 'requestContext' }],
        },
        {
          code: "async ({ page: p }) => { await p.request.get('/api/v1/auth/me') }",
          errors: [{ messageId: 'requestContext' }],
        },
        {
          code: "async ({ request }) => { await request.post('/api/v1/auth/login', { data: {} }) }",
          errors: [{ messageId: 'requestContext' }],
        },
        {
          code: 'async ({ request: api }) => { await api.storageState() }',
          errors: [{ messageId: 'requestContext' }],
        },
        {
          code: 'async ({ request }) => { const api = request; await api.get("/x") }',
          errors: [{ messageId: 'requestContext' }],
        },
        {
          // Taking the context off the page (one report) and calling it
          // through the alias (the second) are both bypasses.
          code: 'async ({ page }) => { const api = page.request; await api.get("/x") }',
          errors: [
            { messageId: 'requestContext' },
            { messageId: 'requestContext' },
          ],
        },
        {
          // Taking a request context off the fixture is itself the bypass.
          code: 'async ({ page }) => { return page.request }',
          errors: [{ messageId: 'requestContext' }],
        },
      ],
    )
  })

  it('flags direct HTTP: fetch, XMLHttpRequest and banned modules', () => {
    runRule(
      [],
      [
        {
          code: "await fetch('/api/v1/auth/me');",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "await window.fetch('/api');",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: 'const captured = globalThis.fetch;',
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: 'const xhr = new XMLHttpRequest();',
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "import axios from 'axios';",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "const mod = await import('node-fetch');",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "const { fetch } = require('node-fetch');",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "import { get } from 'node:http';",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "import { request } from 'node:https';",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "import { createConnection } from 'node:net';",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "import WebSocket from 'ws';",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "import 'ws';",
          errors: [{ messageId: 'directHttp' }],
        },
        {
          code: "const socket = new WebSocket('ws://127.0.0.1:1');",
          errors: [{ messageId: 'directHttp' }],
        },
      ],
    )
  })

  it('flags every CDP domain outside WebAuthn', () => {
    runRule(
      [],
      [
        {
          code: "await session.send('Network.enable');",
          errors: [{ messageId: 'cdp', data: { domain: 'Network' } }],
        },
        {
          code: "await session.send('Runtime.evaluate', { expression: '1' });",
          errors: [{ messageId: 'cdp', data: { domain: 'Runtime' } }],
        },
        {
          code: "await client.send('Page.navigate');",
          errors: [{ messageId: 'cdp', data: { domain: 'Page' } }],
        },
        // A dynamic command on a tracked session cannot be proven WebAuthn.
        {
          code:
            'const session = await context.newCDPSession(page);' +
            'await session.send(process.env.CDP_COMMAND);',
          errors: [{ messageId: 'cdpDynamic' }],
        },
        {
          // The session is recognized by its initializer, then followed
          // through an alias.
          code:
            'const session = await context.newCDPSession(page);' +
            'const s = session;' +
            'await s.send(process.env.CDP_COMMAND);',
          errors: [{ messageId: 'cdpDynamic' }],
        },
        {
          code: 'const browser = await chromium.connectOverCDP(process.env.CDP_URL);',
          errors: [{ messageId: 'connectOverCdp' }],
        },
      ],
    )
  })

  it('flags the assembly layer: process, filesystem writes, databases', () => {
    runRule(
      [],
      [
        {
          code: "import { execFile } from 'node:child_process';",
          errors: [{ messageId: 'assemblyOnly' }],
        },
        {
          code: "import { writeFileSync } from 'node:fs';",
          errors: [{ messageId: 'assemblyOnly' }],
        },
        {
          code: "import fs from 'node:fs';",
          errors: [{ messageId: 'assemblyOnly' }],
        },
        {
          code: "import * as fsp from 'node:fs/promises';",
          errors: [{ messageId: 'assemblyOnly' }],
        },
        {
          code: "import Database from 'better-sqlite3';",
          errors: [{ messageId: 'assemblyOnly' }],
        },
        {
          code: "const { execFile } = require('node:child_process');",
          errors: [{ messageId: 'assemblyOnly' }],
        },
      ],
    )
  })

  it('flags raw log interpretation outside fixtures/log.ts', () => {
    runRule(
      [],
      [
        {
          code: "import { parseLogfmt } from '../fixtures/log';",
          errors: [{ messageId: 'logInterpretation' }],
        },
        {
          code: "import { ProcessLog } from '../fixtures/log';",
          errors: [{ messageId: 'logInterpretation' }],
        },
        {
          code: 'const captured = server.log.text;',
          errors: [{ messageId: 'logInterpretation' }],
        },
        {
          code: 'const lines = server.log.lines;',
          errors: [{ messageId: 'logInterpretation' }],
        },
      ],
    )
  })

  it('allows read-only filesystem and the verification-code reader', () => {
    runRule(
      [
        "import { readFileSync } from 'node:fs';",
        "import { readFile } from 'node:fs/promises';",
        "import { waitForVerificationCode } from '../fixtures/log';",
      ],
      [],
    )
  })

  it('allows the assembly layer to build, seed and probe', () => {
    runRule(
      [
        {
          code: "import { execFile } from 'node:child_process';",
          options: [{ allowAssembly: true }],
        },
        {
          code: "import { mkdtemp, writeFile } from 'node:fs/promises';",
          options: [{ allowAssembly: true }],
        },
        {
          code: "import { get } from 'node:http';",
          options: [{ allowAssembly: true }],
        },
        {
          code: "import { createServer } from 'node:net';",
          options: [{ allowAssembly: true }],
        },
        {
          code: "import { ProcessLog, waitForReady } from './log';",
          options: [{ allowAssembly: true }],
        },
      ],
      [],
    )
  })

  it('reports each violation in mixed code, once per occurrence', () => {
    runRule(
      [],
      [
        {
          code: "await page.evaluate(() => fetch('/x'));",
          errors: [
            { messageId: 'evaluate' },
            { messageId: 'directHttp' },
          ],
        },
      ],
    )
  })
})
