import app from 'flarum/forum/app';

/**
 * The single source every Garrison surface reads.
 *
 * 🚨 ONE poll for the whole page, no matter how many widgets are on it.
 *
 * A page can carry the status page, a sidebar widget, a Bespoke widget and a
 * Page Builder block at once. If each fetched its own data that is four
 * requests per render, and on a busy forum it is four per visitor — which is
 * the failure that once exhausted a database connection cap and 500'd a whole
 * forum. So: one module-level store, one in-flight request shared by every
 * caller, and components subscribe rather than fetch.
 */

let servers = [];
let loaded = false;
let error = null;
let inflight = null;
let timer = null;
const listeners = new Set();

function notify() {
  listeners.forEach((fn) => fn());
  m.redraw();
}

/**
 * Fetch once. Concurrent callers share the same promise rather than each
 * firing their own request — which is the entire point of this module.
 */
export function refresh() {
  if (inflight) return inflight;

  inflight = app
    .request({ method: 'GET', url: app.forum.attribute('apiUrl') + '/garrison/servers' })
    .then((res) => {
      servers = res && Array.isArray(res.data) ? res.data : [];
      loaded = true;
      error = null;
    })
    .catch((e) => {
      // 🚨 A failed poll must not blank the page. The last known state, marked
      // stale, is more useful than an empty panel — and an empty panel reads
      // as "no servers" rather than "could not reach the forum".
      error = e;
      loaded = true;
    })
    .then(() => {
      inflight = null;
      notify();
    });

  return inflight;
}

export function subscribe(fn) {
  listeners.add(fn);

  if (!loaded) refresh();

  // Poll while anybody is watching, and stop the moment nobody is. A forum
  // tab left open overnight must not keep asking.
  if (!timer) {
    timer = setInterval(() => {
      if (document.hidden) return; // a background tab is not watching
      refresh();
    }, 15000);
  }

  return () => {
    listeners.delete(fn);
    if (listeners.size === 0 && timer) {
      clearInterval(timer);
      timer = null;
    }
  };
}

export function all() {
  return servers;
}

/**
 * One server by id, or null while the first poll is still in flight.
 *
 * 🚨 Reads the SAME list, rather than fetching one server by id. A per-server
 * endpoint would be the obvious thing to add for a per-server page, and it
 * would mean two ways to read a server, gated separately — which is how one of
 * them ends up leaking a join password to somebody the other correctly refuses.
 * One payload, shaped once per actor, is the whole design of this store.
 */
export function byId(id) {
  const n = Number(id);

  return servers.find((s) => s.id === n) || null;
}

export function isLoaded() {
  return loaded;
}

export function lastError() {
  return error;
}

/**
 * Queue a command. Returns the request promise so a caller can show its own
 * pending state; the store refreshes shortly after, because the agent has up
 * to a poll window to act and an immediate refetch would show the old state
 * and look like the button did nothing.
 */
export function command(serverId, verb, params = {}) {
  return app
    .request({
      method: 'POST',
      url: app.forum.attribute('apiUrl') + `/garrison/servers/${serverId}/command`,
      body: { verb, params },
    })
    .then((res) => {
      setTimeout(refresh, 2000);
      return res;
    });
}

/**
 * Wait for a queued command to finish, and say how it went.
 *
 * 🚨 Polling, because queueing returns "accepted" and nothing more. The agent
 * has up to a poll window to pick a command up, so a UI that treated 202 as
 * success would tell somebody their world had been restored at the moment
 * nothing had yet happened. For a restore — the one action here that cannot be
 * undone — that is the difference between a panel that reports and one that
 * guesses.
 *
 * Gives up after `attempts`, and a timeout resolves rather than rejects: the
 * command may well still succeed, and "we stopped watching" is a different and
 * more honest thing to say than "it failed".
 */
export function awaitCommand(id, { attempts = 45, everyMs = 2000 } = {}) {
  let left = attempts;

  return new Promise((resolve) => {
    const tick = () => {
      app
        .request({ method: 'GET', url: app.forum.attribute('apiUrl') + `/garrison/commands/${id}` })
        .then((res) => {
          const c = (res && res.data) || {};

          if (c.status === 'done' || c.status === 'failed' || c.status === 'expired') {
            refresh();
            return resolve(c);
          }

          if (--left <= 0) return resolve({ status: 'waiting' });

          setTimeout(tick, everyMs);
        })
        .catch(() => resolve({ status: 'unknown' }));
    };

    setTimeout(tick, everyMs);
  });
}
