import app from 'flarum/forum/app';
import Component from 'flarum/common/Component';
import LoadingIndicator from 'flarum/common/components/LoadingIndicator';
import humanTime from 'flarum/common/helpers/humanTime';

import { serverMark } from '../marks';
import { claim, release, SIDEBAR } from '../placement';
import { all, isLoaded, lastError, subscribe } from '../store';

/**
 * The list of servers, rendered identically wherever it is mounted.
 *
 * 🚨 This component is the "one widget core" of the four-host design. The
 * stock sidebar, fof/forum-widgets-core, Bespoke and Page Builder each mount
 * THIS, and an adapter contributes registration and placement chrome only.
 * Put host-specific behaviour in here and it has to be written four times and
 * will drift three ways.
 */
export default class ServerList extends Component {
  oninit(vnode) {
    super.oninit(vnode);
    this.unsubscribe = null;

    /*
     * 🚨 Read ONCE and kept, rather than read from attrs at teardown. A host
     * that re-renders with different attrs would otherwise release a mount it
     * never claimed and leave the real one counted for ever — which, since the
     * count is what silences the sidebar, means a sidebar that never returns.
     */
    this.host = this.attrs.host || SIDEBAR;
  }

  oncreate(vnode) {
    super.oncreate(vnode);
    this.unsubscribe = subscribe(() => {});
    claim(this.host);
  }

  onremove(vnode) {
    super.onremove(vnode);
    // Leaving the page must stop the polling, or a forum tab open overnight
    // keeps asking for ever.
    if (this.unsubscribe) this.unsubscribe();
    release(this.host);
  }

  view() {
    if (!isLoaded()) {
      return (
        <div className="GarrisonWidget">
          <LoadingIndicator display="inline" size="small" />
        </div>
      );
    }

    const servers = all();

    if (!servers.length) {
      return (
        <div className="GarrisonWidget GarrisonWidget--empty">
          {app.translator.trans(
            lastError()
              ? 'ernestdefoe-garrison.forum.unreachable'
              : 'ernestdefoe-garrison.forum.no_servers'
          )}
        </div>
      );
    }

    return (
      <div className="GarrisonWidget">
        <ul className="GarrisonWidget-list">
          {servers.map((s) => this.row(s))}
        </ul>
      </div>
    );
  }

  row(s) {
    // 🚨 Stale is its own signal, not a state. "Running, last heard from 40
    // minutes ago" is a completely different fact from "running", and the
    // whole product exists because the first one looked like the second for
    // twenty hours.
    const stale = s.stale || s.agentLate;

    /*
     * 🚨 THE WHOLE ROW IS A LINK, and it used to be nothing at all.
     *
     * A widget showing "Shattered Pact · 3/10" with no way through is a dead
     * end: the reader now knows a server exists and has no way to find its
     * address, its players, or why it is unhappy. Every one of those lives on
     * the server's own page, and this was the only place most readers would
     * ever see the server named.
     *
     * The row rather than the name: a 240px sidebar entry is a target people
     * aim at as a whole, and a 90px name inside it is a target they miss.
     */
    return (
      <li className={'GarrisonServer' + (stale ? ' GarrisonServer--stale' : '')} key={s.id}>
        <a
          className="GarrisonServer-link"
          href={app.route('garrison.server', { id: s.id })}
          config={m.route.link}
          title={s.name}
        >
          {/*
            🚨 The dot, the mark and the name are ONE group that never breaks
            up. Left to wrap freely they took three lines in a narrow fof panel
            — the mark alone on one, the name on another, the status on a third
            — which reads as a broken layout. Only the status may drop to a
            second line.
          */}
          <span className="GarrisonServer-identity">
            <span
              className={`GarrisonServer-state GarrisonServer-state--${s.state}`}
              aria-hidden="true"
            />
            {serverMark(s, 18)}
            <span className="GarrisonServer-name">{s.name}</span>
          </span>
          <span className="GarrisonServer-players">{this.detail(s, stale)}</span>
        </a>
      </li>
    );
  }

  detail(s, stale) {
    /*
     * 🚨 The SHORT form here, and the long one only on the page.
     *
     * "Last heard from 37 minutes ago" is eleven words in a 240px sidebar
     * column: it pushed the server's own name down to "Sh…" and "Ba…", which
     * made the widget useless and made the game marks look like they were not
     * rendering at all. The name is the thing somebody is looking for; the
     * detail belongs where there is room for it.
     */
    if (stale && s.lastStatusAt) {
      return app.translator.trans('ernestdefoe-garrison.forum.stale_short');
    }

    // 🚨 In a widget there is room for one word, so it goes to the worst true
    // thing. "3/10" beside a server nobody can join is a lie of omission.
    if (s.needsAttention) {
      return app.translator.trans('ernestdefoe-garrison.forum.health.attention_short');
    }

    if (s.state === 'running' && s.health === 'unready') {
      return app.translator.trans('ernestdefoe-garrison.forum.health.unready_short');
    }

    if (s.state === 'running' && s.playersOnline !== null && s.playersOnline !== undefined) {
      if (s.playersMax) return `${s.playersOnline}/${s.playersMax}`;

      return app.translator.trans('ernestdefoe-garrison.forum.players', { count: s.playersOnline });
    }

    return app.translator.trans(`ernestdefoe-garrison.forum.state.${s.state}`);
  }
}
