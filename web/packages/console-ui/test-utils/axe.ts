/**
 * console-ui's binding of the shared axe assertions.
 *
 * The console is page-level chrome (a banner, a nav landmark and a
 * `main`), so its suites use the page tier: `region` and
 * landing-one-main stay enabled and the scans must answer them, the same
 * tier layout-kit's AppShell suite uses. Re-exported through the shared
 * helper so the rule tiers cannot drift between packages.
 */
export {
  expectNoAxeViolations,
  expectNoAxeViolationsForPage,
} from '@speed/test-utils/axe'
