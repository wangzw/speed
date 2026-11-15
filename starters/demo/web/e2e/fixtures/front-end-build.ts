/**
 * The pnpm command the global setup builds the demo front end with.
 *
 * The demo host (starters/demo/web) is an external member of the pnpm
 * workspace under web/, and no directory above it holds pnpm-workspace.yaml.
 * pnpm locates its workspace by walking up from the directory it runs in, so
 * a build started in the host's own directory installs it as a standalone
 * project and asks the registry for the unpublished @speed packages. The build
 * runs in the workspace root and selects the host by its package name.
 */
import { WEB_WORKSPACE_DIR } from './paths'

/** The demo host's package name, which the workspace filter selects. */
export const FRONT_END_PACKAGE = 'demo-web'

/** The arguments to `pnpm` that build the demo front end. */
export const FRONT_END_BUILD_ARGS: readonly string[] = [
  '-C',
  WEB_WORKSPACE_DIR,
  '--filter',
  FRONT_END_PACKAGE,
  'run',
  'build',
]
