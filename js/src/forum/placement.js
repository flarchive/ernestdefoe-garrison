/**
 * Which widget hosts are showing the server list RIGHT NOW.
 *
 * 🚨 This exists because "a host is installed" and "a host is showing the
 * widget" are different facts, and the stock sidebar was standing down for the
 * first when it should only stand down for the second.
 *
 * On ernestdefoe.online — Bespoke and Page Builder both installed, neither
 * carrying a Garrison block — that gap put the widget NOWHERE. The sidebar
 * suppressed itself for a placement that did not exist, and the only symptom
 * was an absence: no error, no warning, no empty panel to click. An operator
 * would conclude the extension was broken, and the thing that broke it was
 * installing an unrelated theme.
 *
 * So placement is counted where it actually happens — a mounted ServerList —
 * rather than guessed at registration. That makes it self-healing in both
 * directions: drag a Garrison block onto a Bespoke page and the sidebar copy
 * steps aside on the next draw; remove that block and the sidebar comes back.
 *
 * 🚨 And it fails in the safe direction. The old flag's failure mode was zero
 * copies, which is invisible. This one's is two copies, which is obvious on
 * sight and fixable by the person looking at it.
 */

/** host name -> number of live mounts. */
const mounts = new Map();

/** Everything except the stock sidebar counts as somebody else placing it. */
export const SIDEBAR = 'sidebar';

export function claim(host) {
  mounts.set(host, (mounts.get(host) || 0) + 1);

  /*
   * 🚨 A redraw, because this runs in oncreate — AFTER the draw that decided
   * whether to render the sidebar copy. Without it the sidebar keeps its stale
   * answer until something unrelated triggers the next draw, which on a quiet
   * index page can be never.
   *
   * Only on the transition, not on every mount: a page carrying three Garrison
   * blocks would otherwise schedule three redraws for one unchanged answer.
   */
  if (host !== SIDEBAR && mounts.get(host) === 1) m.redraw();
}

export function release(host) {
  const n = (mounts.get(host) || 0) - 1;

  if (n > 0) mounts.set(host, n);
  else mounts.delete(host);

  if (host !== SIDEBAR && n <= 0) m.redraw();
}

/** True when some host other than the stock sidebar is showing the list. */
export function hostedElsewhere() {
  for (const host of mounts.keys()) if (host !== SIDEBAR) return true;

  return false;
}
