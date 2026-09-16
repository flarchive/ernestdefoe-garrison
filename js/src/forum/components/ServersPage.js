import app from 'flarum/forum/app';
import Page from 'flarum/common/components/Page';
import IndexPage from 'flarum/forum/components/IndexPage';
import Button from 'flarum/common/components/Button';
import LoadingIndicator from 'flarum/common/components/LoadingIndicator';
import humanTime from 'flarum/common/helpers/humanTime';

import Console from './Console';
import ServerControls from './ServerControls';
import { bytes } from '../format';
import { serverMark } from '../marks';
import { all, isLoaded, lastError, subscribe } from '../store';

/**
 * The status page: every server, in full.
 *
 * 🚨 Reads the SAME store as the sidebar widget and every other host adapter.
 * If this page fetched its own copy, a forum with the widget in the sidebar
 * and this page open would make two requests where one will do — and the
 * pattern, repeated per widget, is what once exhausted a database connection
 * cap and took a whole forum down.
 */
export default class ServersPage extends Page {
  oninit(vnode) {
    super.oninit(vnode);
    this.unsubscribe = null;
    this.openConsole = null;
    app.history.push('garrison', app.translator.trans('ernestdefoe-garrison.forum.title'));
  }

  oncreate(vnode) {
    super.oncreate(vnode);
    this.unsubscribe = subscribe(() => {});
  }

  onremove(vnode) {
    super.onremove(vnode);
    if (this.unsubscribe) this.unsubscribe();
  }

  view() {
    return (
      <div className="GarrisonPage IndexPage">
        {IndexPage.prototype.hero ? null : null}
        <div className="container">
          <div className="sideNavContainer">
            <div className="IndexPage-results sideNavOffset">
              <h2 className="GarrisonPage-title">
                {app.translator.trans('ernestdefoe-garrison.forum.title')}
              </h2>
              {this.body()}
            </div>
          </div>
        </div>
      </div>
    );
  }

  body() {
    if (!isLoaded()) {
      return <LoadingIndicator />;
    }

    const servers = all();

    if (!servers.length) {
      return (
        <p className="GarrisonPage-empty">
          {app.translator.trans(
            lastError()
              ? 'ernestdefoe-garrison.forum.unreachable'
              : 'ernestdefoe-garrison.forum.no_servers'
          )}
        </p>
      );
    }

    return <ul className="GarrisonCards">{servers.map((s) => this.card(s))}</ul>;
  }

  card(s) {
    const stale = s.stale || s.agentLate;
    const running = s.state === 'running';

    return (
      <li className={'GarrisonCard' + (stale ? ' GarrisonCard--stale' : '')} key={s.id}>
        <div className="GarrisonCard-head">
          <span className={`GarrisonServer-state GarrisonServer-state--${s.state}`} aria-hidden="true" />
          <span className="GarrisonCard-mark">{serverMark(s, 26)}</span>
          {/*
            🚨 The name is the link, rather than a "Details" button beside it.
            The name is the thing somebody's eye is already on and the thing
            they would try to click; adding a separate control would put the
            obvious target next to the working one.
          */}
          <h3 className="GarrisonCard-name">
            <a href={app.route('garrison.server', { id: s.id })} config={m.route.link}>
              {s.name}
            </a>
          </h3>
          <span className="GarrisonCard-state">
            {app.translator.trans(`ernestdefoe-garrison.forum.state.${s.state}`)}
          </span>
          <ServerControls server={s} />
        </div>

        {/*
          🚨 Staleness is said out loud, above everything else on the card.
          "Running, last heard from 40 minutes ago" is a completely different
          fact from "Running", and a panel that confidently shows the second
          when it means the first is precisely how a twenty-hour outage goes
          unnoticed. That outage is why this product exists.
        */}
        {stale ? (
          <p className="GarrisonCard-stale">
            {app.translator.trans('ernestdefoe-garrison.forum.stale', {
              when: s.lastStatusAt ? humanTime(s.lastStatusAt) : '—',
            })}
          </p>
        ) : null}

        {this.health(s, running)}

        {/*
          🚨 CPU and memory ONLY while running. They are the last sample taken,
          and for a stopped server that is a memory of when it was up — a
          stopped process is using no CPU and no memory, so showing "0% / 3.4
          MiB" states something untrue with the same confidence as everything
          else on the card. A fact that is not currently a fact is omitted.
        */}
        <dl className="GarrisonCard-facts">
          {this.fact('players', running && s.playersOnline != null ? (s.playersMax ? `${s.playersOnline}/${s.playersMax}` : String(s.playersOnline)) : null)}
          {this.fact('cpu', running && s.cpuPercent != null ? `${s.cpuPercent}%` : null)}
          {this.fact('memory', running && s.memoryBytes != null ? bytes(s.memoryBytes) + (s.statsApproximate ? ' ≈' : '') : null)}
          {this.fact('uptime', running && s.runningSince ? humanTime(s.runningSince) : null)}
          {this.fact('driver', s.driver)}
        </dl>

        {s.canConsole ? (
          <div className="GarrisonCard-console">
            {Button.component(
              {
                className: 'Button Button--link GarrisonCard-consoleToggle',
                icon: this.openConsole === s.id ? 'fas fa-caret-down' : 'fas fa-caret-right',
                onclick: () => {
                  // Closing unmounts the component, which stops its polling —
                  // see Console.onremove. One console open at a time.
                  this.openConsole = this.openConsole === s.id ? null : s.id;
                },
              },
              app.translator.trans('ernestdefoe-garrison.forum.console')
            )}
            {this.openConsole === s.id ? <Console server={s} /> : null}
          </div>
        ) : null}

        {s.joinAddress || s.joinCode ? (
          <div className="GarrisonCard-join">
            <span className="GarrisonCard-joinLabel">
              {app.translator.trans('ernestdefoe-garrison.forum.join')}
            </span>
            {s.joinAddress ? <code className="GarrisonCard-joinValue">{s.joinAddress}</code> : null}
            {s.joinCode ? (
              <code className="GarrisonCard-joinValue">
                {app.translator.trans('ernestdefoe-garrison.forum.join_code', { code: s.joinCode })}
              </code>
            ) : null}
          </div>
        ) : null}
      </li>
    );
  }

  /**
   * 🚨 The banner this whole product exists for.
   *
   * A server that is RUNNING and UNREADY is the failure that went unnoticed
   * for twenty hours, because every dashboard in the world showed it green.
   * So it is not a subtle tint on a row: it says, in words, that players
   * cannot get in — above the facts, not beside them.
   *
   * And "unknown" is rendered as its own thing, never as healthy. A server
   * with no probes has not been checked, and a green light that means "nobody
   * looked" is worse than no light at all.
   */
  health(s, running) {
    if (s.needsAttention) {
      return (
        <div className="GarrisonHealth GarrisonHealth--attention">
          <strong>{app.translator.trans('ernestdefoe-garrison.forum.health.attention')}</strong>
          <span>{app.translator.trans('ernestdefoe-garrison.forum.health.attention_detail')}</span>
          {this.checks(s)}
        </div>
      );
    }

    if (running && s.health === 'unready') {
      const checks = s.healthChecks || [];

      /*
       * The summary IS the first failing check's name — it is chosen that way
       * on the agent, so that somebody who can only see the state still gets
       * the finding. Printing both puts the same sentence on screen twice,
       * which reads as a bug in the panel rather than a fault on the server.
       * Staff see the detailed list; everyone else sees the one line.
       */
      const summaryIsDuplicated = checks.some((c) => c.name === s.healthSummary);

      return (
        <div className="GarrisonHealth GarrisonHealth--unready">
          <strong>{app.translator.trans('ernestdefoe-garrison.forum.health.unready')}</strong>
          {s.healthSummary && !summaryIsDuplicated ? <span>{s.healthSummary}</span> : null}
          {this.checks(s)}
        </div>
      );
    }

    if (running && (s.health === 'unknown' || !s.health)) {
      return (
        <p className="GarrisonHealth GarrisonHealth--unknown">
          {app.translator.trans('ernestdefoe-garrison.forum.health.unchecked')}
        </p>
      );
    }

    return null;
  }

  /** Failing probes, staff only — the API omits them for everybody else. */
  checks(s) {
    if (!s.healthChecks || !s.healthChecks.length) return null;

    return (
      <ul className="GarrisonHealth-checks">
        {s.healthChecks.map((c, i) => (
          <li key={i}>
            <span className="GarrisonHealth-checkName">{c.name}</span>
            {c.detail ? <span className="GarrisonHealth-checkDetail">{c.detail}</span> : null}
          </li>
        ))}
      </ul>
    );
  }

  /** A fact is omitted entirely when unknown, never rendered as an empty row. */
  fact(key, value) {
    if (value === null || value === undefined || value === '') return null;

    return [
      <dt key={key + '-t'}>{app.translator.trans(`ernestdefoe-garrison.forum.fact.${key}`)}</dt>,
      <dd key={key + '-d'}>{value}</dd>,
    ];
  }
}

