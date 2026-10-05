import app from 'flarum/forum/app';
import Component from 'flarum/common/Component';
import Button from 'flarum/common/components/Button';
import LoadingIndicator from 'flarum/common/components/LoadingIndicator';

import { command } from '../store';

/**
 * A server's console.
 *
 * 🚨 Polls only while it is OPEN, and stops the moment it closes. A console
 * left running behind a collapsed panel is a request every few seconds for as
 * long as the tab exists — on a forum that is per visitor, which is how a
 * panel becomes the busiest thing on the site.
 */
export default class Console extends Component {
  oninit(vnode) {
    super.oninit(vnode);

    this.lines = [];
    this.loading = true;
    this.canSend = false;
    this.sending = false;
    this.draft = '';
    this.since = 0;
    this.timer = null;
    this.pinned = true;
  }

  oncreate(vnode) {
    super.oncreate(vnode);

    this.scroller = vnode.dom.querySelector('.GarrisonConsole-out');
    this.fetch();
    this.timer = setInterval(() => this.fetch(), 5000);
  }

  onremove(vnode) {
    super.onremove(vnode);
    if (this.timer) clearInterval(this.timer);
  }

  onupdate() {
    /*
     * 🚨 Only auto-scroll when the reader is already at the bottom.
     *
     * Somebody scrolled up is reading something. Yanking them back down every
     * five seconds because new output arrived makes a busy console impossible
     * to read — which is precisely when they most need to read it.
     */
    if (this.pinned && this.scroller) {
      this.scroller.scrollTop = this.scroller.scrollHeight;
    }
  }

  fetch() {
    const url =
      app.forum.attribute('apiUrl') +
      '/garrison/servers/' +
      this.attrs.server.id +
      '/console' +
      (this.since ? '?since=' + this.since : '');

    return app
      .request({ method: 'GET', url })
      .then((res) => {
        this.loading = false;
        this.canSend = !!res.canSend;

        if (res.lines && res.lines.length) {
          this.lines = this.lines.concat(res.lines);
          this.since = res.lines[res.lines.length - 1].id;

          // Bounded in the browser too: a console open for an hour on a chatty
          // server would otherwise grow until the tab is unusable.
          if (this.lines.length > 2000) {
            this.lines = this.lines.slice(-2000);
          }
        }

        m.redraw();
      })
      .catch(() => {
        this.loading = false;
        m.redraw();
      });
  }

  view() {
    /*
     * 🚨 A MODIFIER, not an inline height.
     *
     * The per-server page wants a taller console than the card does. Setting
     * `style={{height}}` here would win against every stylesheet — including
     * the phone-width rule that stops the console eating a whole screen — and
     * a component that cannot be restyled from CSS is one that will look wrong
     * on the first surface nobody thought of.
     */
    const tall = this.attrs.tall ? ' GarrisonConsole--tall' : '';

    return (
      <div className={'GarrisonConsole' + tall}>
        <div
          className="GarrisonConsole-out"
          onscroll={(e) => {
            const el = e.target;
            this.pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
          }}
        >
          {this.loading ? (
            <LoadingIndicator display="inline" size="small" />
          ) : this.lines.length === 0 ? (
            <p className="GarrisonConsole-empty">
              {app.translator.trans('ernestdefoe-garrison.forum.console_empty')}
            </p>
          ) : (
            this.lines.map((l) => (
              <div className={'GarrisonConsole-line' + (l.stderr ? ' is-stderr' : '')} key={l.id}>
                {l.text}
              </div>
            ))
          )}
        </div>

        {this.canSend ? (
          <form
            className="GarrisonConsole-send"
            onsubmit={(e) => {
              e.preventDefault();
              this.send();
            }}
          >
            <input
              className="FormControl"
              placeholder={app.translator.trans('ernestdefoe-garrison.forum.console_placeholder')}
              value={this.draft}
              disabled={this.sending}
              oninput={(e) => {
                this.draft = e.target.value;
              }}
            />
            {Button.component(
              { className: 'Button', type: 'submit', loading: this.sending, disabled: !this.draft },
              app.translator.trans('ernestdefoe-garrison.forum.console_send')
            )}
          </form>
        ) : null}
      </div>
    );
  }

  send() {
    const line = this.draft.trim();

    if (!line) return;

    this.sending = true;

    command(this.attrs.server.id, 'console.send', { line })
      .then(() => {
        this.draft = '';
        this.sending = false;
        // 🚨 Do NOT echo the command locally. The server will print it when it
        // actually runs it — and if it never does, an echo would have shown
        // the operator a command that did nothing as though it had worked.
        m.redraw();
      })
      .catch(() => {
        this.sending = false;
        m.redraw();
      });
  }
}
