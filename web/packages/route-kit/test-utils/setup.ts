/**
 * vitest setup for route-kit tests: the workspace's shared setup side
 * effect (see @speed/test-utils/setup) -- the jest-dom matchers plus an
 * explicit per-test cleanup. Nothing package-specific is needed: the
 * route tests drive window.location directly through their own harness
 * (test-utils/render.tsx), not a media-query or resize stub.
 */
import '@speed/test-utils/setup'
