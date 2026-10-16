/**
 * objectstore-ui's scan tier: the widget-tier axe assertion (see
 * @speed/test-utils/axe's header). The page renders its own h1 through
 * ui-kit's PageHeader, so a scan over it answers page-has-heading-one
 * honestly; a fragment rendered on its own supplies a heading in that test.
 */
export { expectNoAxeViolations } from '@speed/test-utils/axe'
