/**
 * The neutral game marks Garrison ships.
 *
 * 🚨 Original glyphs, NOT game logos. "Minecraft", "Valheim" and the rest are
 * trade marks whose logos are not licensed for redistribution inside a
 * commercial product — bundling them would put every customer's forum on the
 * wrong side of that, over decoration. An operator who wants the real logo
 * uploads it; this is what shows until they do.
 *
 * Inline paths rather than files: they are a few hundred bytes each, they must
 * inherit the theme's colour through `currentColor`, and an <img> to a
 * separate asset cannot do that.
 */
const PATHS = {
  // A longship's prow and shield line.
  longship:
    'M2 15c3 3 6 4 10 4s7-1 10-4l-2-1H4l-2 1zM4 13h16l-2-4H6l-2 4zM12 9V3M9 5h6',
  // A cube in isometric.
  block: 'M12 2 3 7v10l9 5 9-5V7l-9-5zM12 2v10m0 0L3 7m9 5 9-5m-9 5v10',
  // A capture sphere: a band across a circle.
  sphere: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0zM3 12h18M12 12a3 3 0 1 0 0-.01',
  // A gear tooth ring.
  gear: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v3m0 14v3M2 12h3m14 0h3M5 5l2 2m10 10 2 2M19 5l-2 2M7 17l-2 2',
  // A three-toed claw.
  claw: 'M6 3c0 6 1 11 4 15M12 2c0 7 0 12 1 16M18 4c-1 6-2 10-4 14M5 20c4 2 10 2 14 0',
  // A crosshair.
  crosshair: 'M12 3v5m0 8v5M3 12h5m8 0h5M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
  // A factory roofline with a chimney.
  factory: 'M3 20h18M3 20v-8l5 3v-3l5 3v-3l5 3v5M17 9V4h3v5',
  // A pickaxe.
  pickaxe: 'M3 7c5-4 13-4 18 0M12 5v15M8 9c2-1 6-1 8 0',
  // Plain server: the unknown-game fallback.
  server:
    'M4 5h16v5H4zM4 14h16v5H4zM7 7.5h.01M7 16.5h.01',
};

/**
 * Render a mark, or null when there is none for this stem.
 *
 * @param {string} stem
 * @param {number} size
 */
export function mark(stem, size = 16) {
  const d = PATHS[stem];

  if (!d) return null;

  return (
    <svg
      className="GarrisonMark"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.6"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d={d} />
    </svg>
  );
}

/**
 * What to draw for a server: the operator's own image if they set one, else a
 * shipped mark, else a monogram of the name.
 */
export function serverMark(server, size = 16) {
  if (server.iconUrl) {
    // 🚨 alt="" and aria-hidden: the server's name is already beside it, and a
    // screen reader announcing "Shattered Pact, image, Shattered Pact" is
    // worse than silence.
    return <img className="GarrisonMark GarrisonMark--custom" src={server.iconUrl} alt="" aria-hidden="true" width={size} height={size} />;
  }

  const drawn = mark(server.mark || 'server', size);

  if (drawn) return drawn;

  return (
    <span className="GarrisonMark GarrisonMark--monogram" aria-hidden="true">
      {server.monogram || '?'}
    </span>
  );
}
