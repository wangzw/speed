/**
 * Generation configuration for the frontend-contract half of the API
 * pipeline: the @speed/api-sdk package's src/ is orval output from the
 * merged platform document.
 *
 * orval version pinned: 8.17.0. Every runner invokes the same command
 * from this directory -- `pnpm dlx orval@8.17.0 --config orval.config.ts`,
 * followed by web/scripts/orval-nodenext-fixup.mjs -- so no two callers
 * can drift. orval itself is never a workspace dependency: pnpm dlx fetches
 * it on demand and it stays out of pnpm-lock.yaml. A version bump must
 * land in this comment and every caller's command together, and
 * shows up as a diff in the regenerated header (which stamps the
 * version).
 *
 * Input is the committed merged document, contracts/api.json, produced by
 * the demo host's `gen` command from starters/demo/server:
 *
 *   go -C starters/demo/server run . \
 *       --config file://$PWD/starters/demo/server/testdata/<stack>.yaml \
 *       <stack> gen --title speed -o ../../../contracts/api.json
 *
 * `-o` is relative to the working directory `go -C` gives the child, which is
 * starters/demo/server, so the path climbs out of the module to reach the root.
 * The document's description and servers come from the host's own defaults, so
 * the command names neither.
 *
 * `gen` merges the OpenAPI fragments the platform modules declare into one
 * document (pkg/ops/openapi.Merge over every module's http.Spec resource),
 * so every platform module with an HTTP fragment is a member of the merged
 * document and this generated surface, which is regenerated wholesale from
 * the fragments and does not rot from being uncalled. The consuming app's
 * own fragments are members of that same document, but its operations
 * carry the application-owner tag the host's gen command writes from the
 * declaring module's provenance; the `input.filters` below excludes that
 * tag, so the platform surface covers only the platform's operations. The
 * app's own API generates into the app's own SDK through a second
 * configuration the app carries, without a filter (it takes both). The tag
 * is a plain operation tag because orval 8.17.0's `input.filters` selects
 * operations by tags; the owner vocabulary itself (`platform` /
 * `application`) is the one contracts/features.json uses.
 * Output covers types AND TanStack Query hooks (client: react-query),
 * written to src/index.ts; generated files carry the DO-NOT-EDIT header
 * below.
 *
 * HTTP transport: httpClient 'axios' selects the axios-shaped mutator
 * call -- one options object `{url, method, headers, params, data,
 * signal}` -- routed to the exported `speedRequest` adapter in
 * packages/api-sdk/src/runtime.ts. That seam (the package's only
 * hand-written file) forwards every call to the request function the
 * host binds from @speed/api-client, so no generated file and no bridge
 * file performs HTTP itself. One operation overrides the mutator:
 * `authn.token.issue` (the token-issuing operation a session refresh
 * uses) routes through `speedRequestCredentialless` instead, which
 * declares the request credential-less (`omitAccessToken` on
 * @speed/api-client's RequestOptions). A session's refresh
 * authenticates with the refresh token in its body and must never
 * present an access token; declaring
 * that per request beats clearing the host's token store for the
 * refresh's duration, which would leave concurrent requests
 * momentarily token-less and turn their bearer-valid 401s into
 * spurious auth failures under the client's bearer-only refresh rule
 * (see the refreshAccessToken doc in @speed/api-client's client.ts).
 * Three orval behaviours are worked around here and documented in the
 * package AGENTS.md: orval emits each mutator import without a file
 * extension (breaking nodenext builds), which
 * web/scripts/orval-nodenext-fixup.mjs rewrites deterministically after
 * every run; the same script's rewrite now covers every mutator name
 * the per-operation override can emit; and query keys carry no tenant
 * prefix by design -- tenant query-key namespacing is a consumer-shell
 * concern, not the SDK's.
 *
 * Config paths are relative to this file's directory (web/).
 */

export default {
  speed: {
    input: {
      target: '../contracts/api.json',
      // Operations the host's own modules declare are in the merged document
      // and carry this tag; the platform package takes only the rest. orval
      // 8.17.0 filters by operation tags (`mode: 'exclude'` keeps everything
      // whose tags do not match), and the matching component schemas are
      // pruned with them.
      filters: {
        mode: 'exclude',
        tags: ['speed.application'],
      },
    },
    output: {
      target: './packages/api-sdk/src/index.ts',
      client: 'react-query',
      httpClient: 'axios',
      mode: 'single',
      override: {
        // Trailing newline is load-bearing: orval concatenates the
        // header with the file body, so the last line must end in \n.
        header: (info: { title: string }): string =>
          `// Code generated by orval 8.17.0. DO NOT EDIT.\n` +
          `// Source: ${info.title}\n` +
          `// The mutator import in this file is rewritten to an explicit\n` +
          `// .js extension by web/scripts/orval-nodenext-fixup.mjs (nodenext\n` +
          `// builds); rerun it after every regeneration.\n`,
        mutator: {
          path: './packages/api-sdk/src/runtime.ts',
          name: 'speedRequest',
        },
        operations: {
          // The one credential-less operation in the surface: a
          // session's refresh authenticates with the refresh token in
          // its body and must never present an access token -- declared
          // per request (`speedRequestCredentialless` sets
          // omitAccessToken) so the host's token store is never cleared
          // for it (rationale above and in runtime.ts).
          'authn.token.issue': {
            mutator: {
              path: './packages/api-sdk/src/runtime.ts',
              name: 'speedRequestCredentialless',
            },
          },
        },
      },
    },
  },
}
