/**
 * Where garrison-pro adds to the admin panel.
 *
 * 🚨 The same queue as the forum's panels.js, and for the same two reasons:
 * the free package must not CONTAIN the paid components (that is what makes
 * this open core rather than a licence check in MIT code), and nothing orders
 * the two bundles, so pro pushes and whichever side runs second drains.
 *
 * Three slots, because pro puts things in three genuinely different places:
 *
 *   page   — a whole section of the panel, level with garrison's own.
 *   host   — per paired host, where provisioning belongs: an install writes to
 *            ONE machine's disk and lands in ONE machine's config.
 *   server — per server, where scheduled work belongs.
 */

const slots = { page: [], host: [], server: [] };

function add(entry) {
  if (!entry || !entry.key || typeof entry.view !== 'function') return;
  if (!slots[entry.slot]) return;
  if (slots[entry.slot].some((e) => e.key === entry.key)) return;

  slots[entry.slot].push(entry);
}

export default function drainAdminQueue() {
  const queued = globalThis.GarrisonAdminQueue;

  if (Array.isArray(queued)) queued.forEach(add);

  globalThis.GarrisonAdminQueue = { push: add };
}

/**
 * Render one slot.
 *
 * 🚨 Keyed, because these are rendered among siblings inside lists that
 * Mithril treats as keyed fragments — an unkeyed vnode beside keyed ones
 * throws inside the renderer and blanks the whole surrounding block, with an
 * error naming Mithril and no file of ours.
 */
export function slotFor(name, attrs) {
  return (slots[name] || [])
    .slice()
    .sort((a, b) => (a.priority || 0) - (b.priority || 0))
    .map((e) => {
      const node = e.view(attrs);

      return node ? { ...node, key: 'pro-' + e.key } : null;
    })
    .filter(Boolean);
}
