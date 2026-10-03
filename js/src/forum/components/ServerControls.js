import app from 'flarum/forum/app';
import Component from 'flarum/common/Component';
import Button from 'flarum/common/components/Button';

import { command } from '../store';

/**
 * Start / stop / restart for one server.
 *
 * 🚨 Two rules govern everything in this file, and both are about not lying to
 * the person pressing the button.
 *
 * 1. A control that cannot do anything is not shown. No Stop on a stopped
 *    server, no Start on a running one. A greyed-out or no-op button is the
 *    commonest bug in this whole family of software: it looks built, it is
 *    worded, it is styled, and it does nothing.
 *
 * 2. The button reports QUEUED, not done. The forum hands the command to a
 *    long-polling agent which has up to a poll window to pick it up, so
 *    flipping the row to "Running" on click would be a guess — and would look
 *    exactly like success on a host that is asleep.
 */
export default class ServerControls extends Component {
  oninit(vnode) {
    super.oninit(vnode);
    this.pending = null;
    this.failed = null;
  }

  view() {
    const server = this.attrs.server;

    if (!server.canControl) return null;

    const running = server.state === 'running';
    const busy = server.state === 'starting' || server.state === 'stopping';

    return (
      <div className="GarrisonControls">
        {this.failed ? <span className="GarrisonControls-error">{this.failed}</span> : null}

        {/* No Start on something already up, and nothing at all mid-transition. */}
        {!running && !busy
          ? this.button('server.start', 'fas fa-play', 'start')
          : null}

        {running && !busy ? this.button('server.restart', 'fas fa-rotate', 'restart') : null}
        {running && !busy ? this.button('server.stop', 'fas fa-stop', 'stop', true) : null}
      </div>
    );
  }

  button(verb, icon, key, confirm = false) {
    const label = app.translator.trans(`ernestdefoe-garrison.forum.action.${key}`);

    return Button.component(
      {
        className: 'Button Button--icon GarrisonControls-button',
        icon,
        title: label,
        'aria-label': label,
        loading: this.pending === verb,
        disabled: this.pending !== null,
        onclick: () => this.run(verb, confirm),
      },
      ''
    );
  }

  run(verb, confirm) {
    const server = this.attrs.server;

    // 🚨 Stopping a server throws people out of a game. Worth one question —
    // and worth naming the server, because a panel with several rows is
    // exactly where somebody clicks the wrong one.
    if (confirm) {
      const question = app.translator.trans('ernestdefoe-garrison.forum.confirm_stop', {
        name: server.name,
      });

      if (!window.confirm(extract(question))) return;
    }

    this.pending = verb;
    this.failed = null;

    command(server.id, verb)
      .then(() => {
        // Left pending on purpose. The row clears it when the agent's next
        // report actually changes the state, so the spinner means "waiting for
        // the host", which is the truth.
        setTimeout(() => {
          this.pending = null;
          m.redraw();
        }, 8000);
      })
      .catch((e) => {
        this.pending = null;
        this.failed = readError(e);
        m.redraw();
      });
  }
}

/** Flatten a translator result to a plain string for window.confirm. */
function extract(value) {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map(extract).join('');
  if (value && value.children) return extract(value.children);
  if (value && value.text) return value.text;
  return '';
}

/**
 * 🚨 Show the server's OWN reason where there is one. "Something went wrong"
 * for a refusal that said `not_permitted` wastes the reader's evening.
 */
function readError(e) {
  const detail = e?.response?.errors?.[0];

  if (detail?.detail) return detail.detail;
  if (detail?.code) return detail.code;

  return extract(app.translator.trans('ernestdefoe-garrison.forum.action_failed'));
}
