/**
 * Where garrison-pro puts its panels on a server's page.
 *
 * 🚨 The free package does not import the paid components — it does not
 * CONTAIN them. That is the whole of open core: the boundary is which package
 * you have, not a flag inside one package that decides whether to render.
 * A build that shipped Backups and then hid it would be a licence check with
 * extra steps, and this bundle is MIT, so anybody could delete the check.
 *
 * 🚨 A QUEUE, not a function to call, and for the same reason hosts.js uses
 * one: the two bundles are separate files and nothing orders them. Pro may
 * register before this module has ever run. So pro always pushes onto an array
 * that it creates if absent, and whichever side runs second drains it.
 */

/** @typedef {{key: string, priority?: number, view: (server: object) => any}} Panel */

/** @type {Panel[]} */
const panels = [];

function add(panel) {
  if (!panel || typeof panel.view !== 'function' || !panel.key) return;

  // Registering twice would draw the panel twice. A bundle can be evaluated
  // more than once in development, and a duplicated Backups panel is a
  // confusing bug to chase back to its cause.
  if (panels.some((p) => p.key === panel.key)) return;

  panels.push(panel);
}

/**
 * Take everything pro queued, and make later pushes register immediately.
 *
 * Called once from the forum initializer.
 */
export default function drainPanelQueue() {
  const queued = globalThis.GarrisonPanelQueue;

  if (Array.isArray(queued)) queued.forEach(add);

  /*
   * Replaced with an object whose `push` registers directly, so a bundle that
   * loads after this point still works and still looks like an array to the
   * code doing the pushing. Same trick as the widget host queues.
   */
  globalThis.GarrisonPanelQueue = { push: add };
}

/**
 * The panels to draw for one server, in order.
 *
 * 🚨 Every panel MUST come back with a key, because ServerPage renders its
 * children as a keyed fragment and Mithril refuses a fragment whose children
 * are part keyed and part not — a `null` counts as unkeyed, and the throw
 * happens inside the renderer, so the whole page body renders as nothing with
 * an error that names Mithril and no file of ours. See ServerPage's own note;
 * it cost an afternoon once already.
 */
export function panelsFor(server) {
  return panels
    .slice()
    .sort((a, b) => (a.priority || 0) - (b.priority || 0))
    .map((p) => {
      const node = p.view(server);

      return node ? { ...node, key: 'pro-' + p.key } : null;
    })
    .filter(Boolean);
}
