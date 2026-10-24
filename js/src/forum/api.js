import { bytes, duration } from './format';
import { awaitCommand, byId, command, refresh, subscribe } from './store';

/**
 * The surface garrison-pro is allowed to build against.
 *
 * 🚨 A runtime global, because the two packages are separate bundles and
 * neither can import from the other at build time.
 *
 * 🚨 And `command`/`awaitCommand` are the REASON this exists rather than pro
 * simply copying two small helper files.
 *
 * The store is not a utility, it is an instance: one poll loop, one cache of
 * servers, one set of subscribers. A pro bundle with its own copy would run a
 * SECOND poll against the same endpoint — doubling the request rate on every
 * forum that bought the paid tier, which is precisely the wrong direction.
 * There is a note in this codebase's history about 34 requests on one page
 * exhausting a database connection cap and 500ing a whole forum; the fix is
 * always one shared thing rather than many correct copies of it.
 *
 * 🚨 This is a PUBLISHED INTERFACE. Garrison is MIT and pro ships separately,
 * so the two can be on different versions on a real forum: a customer updates
 * one and not the other, or Composer resolves an older garrison than pro was
 * built against. Renaming or removing anything here breaks a paid install,
 * silently, at whatever moment somebody opens a backups panel. Add rather than
 * change, and treat `version` as the thing pro checks when that stops being
 * enough.
 */
export default function exposeApi() {
  globalThis.Garrison = Object.freeze({
    version: 1,

    // the shared store — never re-implement these in another bundle
    command,
    awaitCommand,
    refresh,
    subscribe,
    byId,

    // pure formatting, exposed so the two halves agree on what "1.4 GiB" looks
    // like rather than each rounding it their own way
    bytes,
    duration,
  });
}
