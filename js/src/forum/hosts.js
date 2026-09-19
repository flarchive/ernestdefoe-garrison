/**
 * The widget-host adapters.
 *
 * 🚨 One widget core, four host adapters — deliberately the same shape as the
 * supervisor driver on the agent side. Each function below does registration
 * and placement chrome and NOTHING else; the thing being placed is always the
 * same ServerList component.
 *
 * 🚨 Not one of these may be a build-time dependency. Every host is resolved
 * at RUNTIME through its own registry, so the bundle is identical whether the
 * operator has all four installed or none, and importing a package that is not
 * there can never break the forum's whole JS bundle.
 */

/**
 * `flarum` is a global the bundle never imports. Reached through globalThis so
 * that its absence is `undefined` rather than a ReferenceError, which would
 * throw out of an initializer and take the forum's whole JS bundle with it.
 */
function flarumGlobal() {
  return typeof globalThis !== 'undefined' ? globalThis.flarum : undefined;
}

/**
 * Register with every host that is present.
 *
 * 🚨 Registering with a host is NOT placing a widget in it. Every host here
 * gives the operator the OPTION of a Garrison widget; whether they took it is
 * answered later, by placement.js, from mounts that actually exist. An earlier
 * version answered it here — and a forum with Bespoke installed but no
 * Garrison block placed got no widget anywhere at all, because the stock
 * sidebar had stood down for a placement nobody made.
 *
 * @param {object} app
 * @param {(host: string) => any} makeContent renders the shared ServerList,
 *   tagged with the host placing it
 */
export default function registerWidgetHosts(app, makeContent) {
  registerFofWidget(app, makeContent);
  registerBespoke(app, makeContent);
  registerPageBuilder(app, makeContent);
}

/**
 * fof/forum-widgets-core.
 *
 * ⚠️ The package is `fof/forum-widgets-core` and its extension id is
 * `fof-forum-widgets-core` — not `fof/widgets-core`, which is a different
 * (older) thing and the name everybody reaches for first.
 */
function registerFofWidget(app, makeContent) {
  const flarum = flarumGlobal();

  if (!flarum?.extensions || !('fof-forum-widgets-core' in flarum.extensions)) return false;

  const widgetsMod = flarum.reg?.get?.('fof-forum-widgets-core', 'common/extend/Widgets');
  const widgetMod = flarum.reg?.get?.('fof-forum-widgets-core', 'common/components/Widget');
  const Widgets = widgetsMod?.default ?? widgetsMod;
  const WidgetBase = widgetMod?.default ?? widgetMod;

  // Present but not resolvable means a version whose internals moved. Decline
  // quietly and let the stock sidebar carry it, rather than throwing out of an
  // initializer and taking the forum's JS down.
  if (!Widgets || !WidgetBase) return false;

  class GarrisonServersWidget extends WidgetBase {
    className() {
      return 'GarrisonFofWidget';
    }

    icon() {
      return 'fas fa-tower-observation';
    }

    title() {
      return app.translator.trans('ernestdefoe-garrison.forum.title');
    }

    content() {
      return makeContent('fof');
    }
  }

  new Widgets()
    .add({
      key: 'garrisonServers',
      component: GarrisonServersWidget,
      isDisabled: false,
      isUnique: true,
      placement: 'end',
      position: 1,
    })
    .extend(app, 'ernestdefoe-garrison');

  return true;
}

/**
 * Page Builder.
 *
 * 🚨 The ONLY host of the four with a server half, and this is just the client
 * half — src/Widget/ServerStatusBlock.php is the other, registered from
 * extend.php and responsible for deciding what this actor may see. So this
 * component renders what it was HANDED and never asks for more: the join
 * details are in `block.data` only when the server decided they should be.
 *
 * 🚨 Registered through `window.PageBuilderBlockQueue`, not by touching the
 * registry directly, for the same reason Bespoke uses a queue: which of two
 * extensions initialises first is not something either of them decides, and a
 * block registered too early is a block missing from the page with nothing in
 * the console to say why.
 *
 * 🚨 This half did not exist until it was tested against a real Page Builder
 * install. The PHP half registered, resolved and gated correctly all along —
 * which is exactly why nobody noticed: every test that could be written
 * without the other extension passed.
 */
function registerPageBuilder(app, makeContent) {
  const flarum = flarumGlobal();

  if (!flarum?.extensions || !('ernestdefoe-page-builder' in flarum.extensions)) return false;

  globalThis.PageBuilderBlockQueue = globalThis.PageBuilderBlockQueue || [];

  globalThis.PageBuilderBlockQueue.push({
    // 🚨 Matches ServerStatusBlock::type() exactly. A mismatch is silent: the
    // server resolves data for a block the client never renders.
    type: 'garrison-server-status',
    component: {
      view(vnode) {
        const settings = vnode.attrs?.block?.settings || {};

        return (
          <div className={'GarrisonPageBuilderBlock' + (settings.compact ? ' is-compact' : '')}>
            {makeContent('page-builder')}
          </div>
        );
      },
    },
  });

  return true;
}

/**
 * Bespoke.
 *
 * 🚨 Registered through `window.BespokeWidgetQueue`, NOT by calling
 * `app.bespoke.widgets.add` directly. The queue is drained on every registry
 * lookup, so it does not matter whether Garrison's initializer runs before or
 * after Bespoke's — and initializer order between two extensions is not
 * something either of them controls.
 */
function registerBespoke(app, makeContent) {
  const flarum = flarumGlobal();

  if (!flarum?.extensions || !('ernestdefoe-bespoke' in flarum.extensions)) return false;

  /*
   * 🚨 TRANSLATION KEYS, not translated text.
   *
   * Bespoke's contract is a string: either a dotted key it resolves from the
   * owning extension's locale, or a plain English literal. Passing what
   * `trans()` returns breaks that in a way that is easy to miss, because
   * `trans()` hands back an ARRAY of vnodes — the widget's name happens to
   * survive (an array concatenates into a string), while its settings panel
   * calls `.toLowerCase()` on the label and an array does not have one.
   *
   * Keys are also the better answer on their own terms: Bespoke resolves them
   * when it draws the editor, in the reader's own locale, rather than at
   * whatever moment this initializer happened to run.
   */
  const def = {
    type: 'garrison-servers',
    label: 'ernestdefoe-garrison.forum.title',
    icon: 'fas fa-tower-observation',
    zones: ['sidebar', 'above', 'below'],
    schema: [
      {
        key: 'heading',
        type: 'text',
        label: 'ernestdefoe-garrison.forum.widget_heading',
        default: '',
      },
    ],
    component: {
      view(vnode) {
        const heading = vnode.attrs?.settings?.heading;

        return (
          <div className="GarrisonBespokeWidget">
            {heading ? <h4 className="GarrisonSidebar-title">{heading}</h4> : null}
            {makeContent('bespoke')}
          </div>
        );
      },
    },
  };

  globalThis.BespokeWidgetQueue = globalThis.BespokeWidgetQueue || [];
  globalThis.BespokeWidgetQueue.push(def);

  return true;
}
