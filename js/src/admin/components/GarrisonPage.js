import app from 'flarum/admin/app';
import ExtensionPage from 'flarum/admin/components/ExtensionPage';

import { slotFor } from '../slots';
import { extract } from '../text';
import Button from 'flarum/common/components/Button';
import LoadingIndicator from 'flarum/common/components/LoadingIndicator';
import Switch from 'flarum/common/components/Switch';
import humanTime from 'flarum/common/helpers/humanTime';

/**
 * The admin screen: pair hosts, and set the things an operator owns.
 *
 * 🚨 It never lets an admin edit what the AGENT reports — state, health,
 * driver, the server's ref. Those are facts from the host; an admin screen
 * that could write them would put the forum's idea of a server permanently out
 * of step with reality, with no way to tell which was lying.
 */
export default class GarrisonPage extends ExtensionPage {
  oninit(vnode) {
    super.oninit(vnode);

    this.state = null;
    this.loading = true;
    this.pairing = false;
    this.newToken = null;
    this.saving = {};
    this.load();
  }

  load() {
    return app
      .request({ method: 'GET', url: app.forum.attribute('apiUrl') + '/garrison/admin/state' })
      .then((res) => {
        this.state = res;
        this.loading = false;
        m.redraw();
      })
      .catch(() => {
        this.loading = false;
        m.redraw();
      });
  }

  content() {
    if (this.loading) return <LoadingIndicator />;
    if (!this.state) return <p className="GarrisonAdmin-error">{this.t('load_failed')}</p>;

    return (
      <div className="GarrisonAdmin ExtensionPage-settings">
        <div className="container">
          {this.tokenNotice()}
          {this.alerts()}
          {this.hosts()}
          {this.servers()}
          {slotFor('page', { identities: this.state.identities || [], servers: this.state.servers || [], onchange: () => this.load() })}
          {this.incidents()}
        </div>
      </div>
    );
  }

  t(key, params) {
    return app.translator.trans('ernestdefoe-garrison.admin.' + key, params);
  }

  /**
   * 🚨 Shown once, and it says so loudly.
   *
   * Nothing stores the plaintext token — only a hash of it — so an operator
   * who closes this without copying it has to unpair and pair again. Saying
   * that at the moment they can still act on it is the difference between a
   * clear instruction and a support thread.
   */
  tokenNotice() {
    if (!this.newToken) return null;

    return (
      <div className="GarrisonAdmin-token Alert Alert--success">
        <h3>{this.t('token_heading', { name: this.newToken.name })}</h3>
        <p>{this.t('token_once')}</p>
        <code className="GarrisonAdmin-tokenValue">{this.newToken.token}</code>
        <p className="GarrisonAdmin-tokenHelp">{this.t('token_next')}</p>
        {Button.component(
          { className: 'Button', onclick: () => { this.newToken = null; } },
          this.t('token_copied')
        )}
      </div>
    );
  }

  /**
   * 🚨 One field, above the hosts, because it is the setting most likely to
   * matter at 3am. A Flarum notification reaches somebody who opens the forum;
   * an outage overnight is noticed by whoever has Discord on their phone.
   */
  alerts() {
    return (
      <section className="GarrisonAdmin-section">
        <h2>{this.t('alerts')}</h2>
        {this.delivery()}
        <div className="GarrisonAdmin-field">
          <label for="garrison-webhook">{this.t('webhook')}</label>
          <input
            id="garrison-webhook"
            className="FormControl"
            placeholder="https://discord.com/api/webhooks/…"
            bidi={this.setting('ernestdefoe-garrison.webhook_url')}
          />
          <span className="GarrisonAdmin-meta">{this.t('webhook_help')}</span>
        </div>
        {this.submitButton()}
      </section>
    );
  }

  /**
   * 🚨 Whether an alert could actually get out, stated plainly.
   *
   * Every notification Garrison sends is a job on the forum's queue, and a
   * forum whose queue worker is dead loses all of them without a sound —
   * `sync()` returns happily, nothing is logged, and no notification is ever
   * written. The panel would stay green right through the outage this product
   * exists to catch. It happened on the forum Garrison was built on, for three
   * days, and the only reason it was found was somebody going looking.
   *
   * So the answer here is MEASURED, not inferred from configuration: once a
   * minute Garrison pushes a heartbeat job down the same road an alert takes,
   * and this reports whether a worker has run one. "A job pushed a minute ago
   * has been run" is the same sentence an alert needs to be true.
   */
  delivery() {
    const h = this.state.health;

    if (!h) return null;

    /*
     * 🚨 TWO lines, because they have two completely different fixes — a cron
     * entry and a worker process — and one combined "something is wrong" would
     * send an operator to look at the wrong thing half the time.
     *
     * The scheduler comes first because it is the one that matters more and
     * the one more often missing: without it, Garrison checks nothing,
     * restarts nothing, backs up nothing and reports nothing, while looking
     * entirely normal.
     */
    /*
     * 🚨 `.filter(Boolean)` for the same reason ServerPage needs it: Mithril
     * treats a `null` as UNKEYED, and one null beside keyed siblings throws
     * "In fragments, vnodes must either all have keys or none have keys" from
     * inside the renderer — blanking the block with an error that names no file
     * of ours. `machinery()` returns null for a verdict the payload did not
     * carry, which is exactly what an older agent or a partial response gives.
     */
    return [
      this.machinery('scheduler', h.scheduler),
      this.machinery('queue', h.queue),
    ].filter(Boolean);
  }

  /**
   * 🚨 The parameter is `status`, and it must never be named `m`.
   *
   * JSX compiles to `m(...)` calls, so a parameter called `m` shadows
   * Mithril's global inside the very function whose body is JSX — and the
   * failure is `TypeError: m is not a function` pointing at a line that
   * contains no call at all. It cost a round trip here: the block rendered as
   * nothing, and the error named the method rather than the parameter.
   */
  machinery(what, status) {
    if (!status) return null;

    // 🚨 `unknown` is not a warning. Before the first scheduled tick there is
    // no evidence either way, and a panel that cries wolf on a fresh install
    // teaches an operator to ignore the one time it means something.
    const tone = { ok: 'ok', stalled: 'bad', unknown: 'muted' }[status.state] || 'muted';

    return (
      <div className={'GarrisonAdmin-delivery GarrisonAdmin-delivery--' + tone} key={what}>
        <div className="GarrisonAdmin-delivery-headline">
          {app.translator.trans(`ernestdefoe-garrison.admin.machinery.${what}.${status.state}`)}
        </div>

        {/*
          🚨 The fix, in the same block as the problem. "Alerts are not getting
          through" without the next sentence is a puzzle; with it, it is a task.
        */}
        {status.state === 'stalled' ? (
          <div className="GarrisonAdmin-meta">
            {app.translator.trans(`ernestdefoe-garrison.admin.machinery.${what}.stalled_help`)}
          </div>
        ) : null}

        {status.at ? (
          <div className="GarrisonAdmin-meta">
            {app.translator.trans('ernestdefoe-garrison.admin.machinery.last', { when: humanTime(status.at) })}
          </div>
        ) : null}
      </div>
    );
  }

  hosts() {
    return (
      <section className="GarrisonAdmin-section">
        <h2>{this.t('hosts')}</h2>

        <div className="GarrisonAdmin-pair">
          <input
            className="FormControl"
            id="garrison-new-host"
            placeholder={this.t('host_name_placeholder')}
            value={this.hostName || ''}
            oninput={(e) => { this.hostName = e.target.value; }}
          />
          {Button.component(
            {
              className: 'Button Button--primary',
              loading: this.pairing,
              disabled: !this.hostName,
              onclick: () => this.pair(),
            },
            this.t('pair')
          )}
        </div>

        {this.state.agents.length === 0 ? (
          <p className="GarrisonAdmin-empty">{this.t('no_hosts')}</p>
        ) : (
          <ul className="GarrisonAdmin-list">
            {this.state.agents.map((a) => (
              <li className="GarrisonAdmin-host" key={a.id}>
                <div>
                  <strong>{a.name}</strong>
                  <span className="GarrisonAdmin-meta">
                    {/* An agent that has gone quiet is itself an incident — it
                        is how you find out a host died rather than a game. */}
                    {a.lastSeenAt
                      ? this.t(a.late ? 'host_late' : 'host_seen', { when: humanTime(a.lastSeenAt) })
                      : this.t('host_never')}
                    {a.version ? ` · ${a.version} · ${a.os}/${a.arch}` : ''}
                    {a.drivers && a.drivers.length ? ` · ${a.drivers.join(', ')}` : ''}
                  </span>
                </div>
                {Button.component(
                  { className: 'Button Button--danger', onclick: () => this.unpair(a) },
                  this.t('unpair')
                )}

                {/*
                  🚨 Per HOST, because that is what a template belongs to. An
                  install writes to one machine's disk, downloads over one
                  machine's connection, and lands in one machine's config — a
                  forum-wide "add a server" button would have to ask which host
                  anyway, one question later than it needs to.
                */}
                {slotFor('host', { agent: a, servers: this.state.servers || [], onchange: () => this.load() })}
              </li>
            ))}
          </ul>
        )}
      </section>
    );
  }

  servers() {
    if (!this.state.servers.length) return null;

    return (
      <section className="GarrisonAdmin-section">
        <h2>{this.t('servers')}</h2>
        {this.state.servers.map((s) => this.server(s))}
      </section>
    );
  }

  server(s) {
    return (
      <div className="GarrisonAdmin-server" key={s.id}>
        <div className="GarrisonAdmin-serverHead">
          <strong>{s.name}</strong>
          <span className="GarrisonAdmin-meta">
            {s.ref} · {s.driver}
            {s.game ? ` · ${s.game}` : ''}
          </span>
        </div>

        {/* 🚨 The only way needs_attention is ever cleared. The ladder does not
            re-arm itself: it stopped because restarting did not help, and a
            server that looks fine one poll later has proved nothing. */}
        {s.needsAttention ? (
          <div className="GarrisonAdmin-attention">
            <span>{this.t('attention')}</span>
            {Button.component(
              { className: 'Button', onclick: () => this.save(s, { clear_attention: true }) },
              this.t('clear_attention')
            )}
          </div>
        ) : null}

        <div className="GarrisonAdmin-fields">
          {this.field(s, 'name', 'field_name')}
          {this.field(s, 'joinAddress', 'field_join_address', 'join_address')}
          {this.field(s, 'joinPassword', 'field_join_password', 'join_password')}
          {this.field(s, 'joinCode', 'field_join_code', 'join_code')}
        </div>

        <div className="GarrisonAdmin-field">
          <label for={`garrison-${s.id}-backup`}>{this.t('backup_every')}</label>
          <input
            id={`garrison-${s.id}-backup`}
            className="FormControl"
            type="number"
            min="0"
            max="720"
            value={s.backupEveryHours ?? 0}
            oninput={(e) => { s.backupEveryHours = e.target.value; }}
            onblur={() => this.save(s, { backup_every_hours: s.backupEveryHours })}
          />
          <span className="GarrisonAdmin-meta">{this.t('backup_every_help')}</span>
        </div>

        {/*
          🚨 Below the backup interval, because the two are different answers
          to the same question and an operator should see them together. "Every
          6 hours" needs no clock and no timezone; "05:00 on weekdays" needs
          both. Neither subsumes the other, and hiding one behind the other
          would make somebody set up the wrong one and wonder why it never ran
          when they expected.
        */}
        {slotFor('server', {
          server: s,
          schedules: this.state.schedules || [],
          identities: this.state.identities || [],
          onchange: () => this.load(),
        })}

        <div className="GarrisonAdmin-toggles">
          {Switch.component(
            {
              state: s.isPublic,
              onchange: (v) => { s.isPublic = v; this.save(s, { is_public: v }); },
            },
            this.t('public')
          )}
          {Switch.component(
            {
              state: s.autoRemediate,
              onchange: (v) => { s.autoRemediate = v; this.save(s, { auto_remediate: v }); },
            },
            this.t('auto_remediate')
          )}
        </div>

        <div className="GarrisonAdmin-icon">
          <div className="GarrisonAdmin-iconRow">
            {s.iconUrl ? (
              <img className="GarrisonAdmin-iconPreview" src={s.iconUrl} alt="" />
            ) : null}

            <div className="GarrisonAdmin-iconActions">
              {/*
                🚨 The one-click path first, because it is the one that gives
                an operator the game's ACTUAL logo. It downloads and keeps a
                copy here — not a hotlink, which is somebody else's server
                deciding when your forum breaks.
              */}
              {s.canFetchLogo
                ? Button.component(
                    {
                      className: 'Button',
                      icon: 'fas fa-download',
                      loading: this.fetching === s.id,
                      onclick: () => this.fetchLogo(s),
                    },
                    this.t('fetch_logo')
                  )
                : null}

              <label className="GarrisonAdmin-iconLabel">
                <span>{this.t('icon')}</span>
                <input
                  type="file"
                  accept="image/png,image/jpeg,image/gif,image/webp"
                  onchange={(e) => this.uploadIcon(s, e.target.files[0])}
                />
              </label>
            </div>
          </div>

          {/*
            🚨 The catch-all, and the reason it exists: Minecraft has no Steam
            page and is the most common dedicated server there is. Rather than
            guess a URL per game — the one I tried was unreachable when checked
            from the forum host — an operator pastes a link once. It is
            DOWNLOADED and kept, exactly like the catalogue path, so it can
            never break later.
          */}
          <div className="GarrisonAdmin-iconUrl">
            <label for={`garrison-${s.id}-logo-url`}>{this.t('logo_url')}</label>
            <div className="GarrisonAdmin-iconUrlRow">
              <input
                id={`garrison-${s.id}-logo-url`}
                className="FormControl"
                placeholder="https://…"
                value={s.logoUrlDraft || ''}
                oninput={(e) => { s.logoUrlDraft = e.target.value; }}
              />
              {Button.component(
                {
                  className: 'Button',
                  loading: this.fetching === s.id,
                  disabled: !s.logoUrlDraft,
                  onclick: () => this.fetchLogo(s, s.logoUrlDraft),
                },
                this.t('fetch')
              )}
            </div>
          </div>

          <span className="GarrisonAdmin-meta">
            {s.canFetchLogo ? this.t('fetch_logo_help') : this.t('logo_url_help')}
          </span>
        </div>
      </div>
    );
  }

  field(s, key, label, apiKey) {
    const id = `garrison-${s.id}-${key}`;

    return (
      <div className="GarrisonAdmin-field">
        <label for={id}>{this.t(label)}</label>
        <input
          id={id}
          className="FormControl"
          value={s[key] || ''}
          oninput={(e) => { s[key] = e.target.value; }}
          onblur={() => this.save(s, { [apiKey || key]: s[key] })}
        />
      </div>
    );
  }

  /**
   * 🚨 What was tried and what happened — not just "it broke".
   *
   * An incident that says only "the server was down" tells an operator nothing
   * they did not already know. One that says "unready at 05:11, restarted at
   * 05:14, healthy at 05:16" is the difference between trusting automatic
   * remediation and switching it off.
   */
  incidents() {
    return (
      <section className="GarrisonAdmin-section">
        <h2>{this.t('incidents')}</h2>

        {this.state.incidents.length === 0 ? (
          <p className="GarrisonAdmin-empty">{this.t('no_incidents')}</p>
        ) : (
          <ul className="GarrisonAdmin-incidents">
            {this.state.incidents.map((i) => {
              const server = this.state.servers.find((s) => s.id === i.serverId);

              return (
                <li className={'GarrisonAdmin-incident is-' + i.status} key={i.id}>
                  <div className="GarrisonAdmin-incidentHead">
                    <strong>{server ? server.name : '#' + i.serverId}</strong>
                    <span className="GarrisonAdmin-meta">
                      {i.status} · {i.startedAt ? humanTime(i.startedAt) : ''}
                      {i.restarts ? ` · ${i.restarts} restart(s)` : ''}
                    </span>
                  </div>
                  <div className="GarrisonAdmin-incidentCause">{i.cause}</div>
                  {i.actions && i.actions.length ? (
                    <ol className="GarrisonAdmin-incidentActions">
                      {i.actions.map((a, n) => (
                        <li key={n}>{a.what}</li>
                      ))}
                    </ol>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    );
  }

  pair() {
    this.pairing = true;

    app
      .request({
        method: 'POST',
        url: app.forum.attribute('apiUrl') + '/garrison/admin/agents',
        body: { name: this.hostName },
      })
      .then((res) => {
        this.newToken = res;
        this.hostName = '';
        this.pairing = false;
        return this.load();
      })
      .catch(() => {
        this.pairing = false;
        m.redraw();
      });
  }

  unpair(agent) {
    // Names what else goes, because an operator seeing servers vanish they did
    // not expect to lose will assume something broke.
    if (!confirm(extract(this.t('unpair_confirm', { name: agent.name, count: agent.servers })))) return;

    app
      .request({ method: 'DELETE', url: app.forum.attribute('apiUrl') + '/garrison/admin/agents/' + agent.id })
      .then(() => this.load());
  }

  save(server, changes) {
    this.saving[server.id] = true;

    return app
      .request({
        method: 'PATCH',
        url: app.forum.attribute('apiUrl') + '/garrison/admin/servers/' + server.id,
        body: changes,
      })
      .then(() => {
        this.saving[server.id] = false;
        if (changes.clear_attention) return this.load();
        m.redraw();
      })
      .catch(() => {
        this.saving[server.id] = false;
        m.redraw();
      });
  }

  fetchLogo(server, url) {
    this.fetching = server.id;

    app
      .request({
        method: 'POST',
        url: app.forum.attribute('apiUrl') + '/garrison/admin/servers/' + server.id + '/fetch-icon',
        body: url ? { url } : {},
      })
      .then((res) => {
        server.iconUrl = res.iconUrl;
        server.logoUrlDraft = '';
        this.fetching = null;
        m.redraw();
      })
      .catch(() => {
        // The error surfaces through Flarum's own alert; clearing the spinner
        // is all that is left to do, and leaving it spinning would be the
        // worse failure.
        this.fetching = null;
        m.redraw();
      });
  }

  uploadIcon(server, file) {
    if (!file) return;

    const body = new FormData();
    body.append('icon', file);

    app
      .request({
        method: 'POST',
        url: app.forum.attribute('apiUrl') + '/garrison/admin/servers/' + server.id + '/icon',
        serialize: (raw) => raw,
        body,
      })
      .then((res) => {
        server.iconUrl = res.iconUrl;
        m.redraw();
      });
  }
}

