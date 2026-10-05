import app from 'flarum/admin/app';

import drainAdminQueue from './slots';

/*
 * 🚨 `export { default as extend }`, NOT `export * from './extend'`.
 *
 * `export *` does not re-export a default export. The file compiles, the
 * bundle contains every line of the settings screen, and Flarum simply never
 * sees an extender — so the extension page falls back to core's own and says
 * "This extension has no settings", which reads as a page that was never
 * written rather than one that was never reached.
 */
export { default as extend } from './extend';

/*
 * The page and the permissions are registered by the Admin extender in
 * extend.js. Nothing else has to happen at boot, but the initializer stays so
 * the extension reports as initialised rather than as a silent no-op.
 */
app.initializers.add('ernestdefoe-garrison', () => {
  /*
   * Collect whatever garrison-pro queued for the admin panel. See slots.js:
   * the two bundles are separate files and nothing orders them, so pro pushes
   * and whichever side runs second drains.
   */
  drainAdminQueue();
});
