import app from 'flarum/forum/app';
import { extend } from 'flarum/common/extend';

import LinkButton from 'flarum/common/components/LinkButton';

import { GarrisonServer } from './models';
import ServerIncidentNotification from './components/ServerIncidentNotification';
import ServerList from './components/ServerList';
import ServerPage from './components/ServerPage';
import ServersPage from './components/ServersPage';
import registerWidgetHosts from './hosts';
import exposeApi from './api';
import drainPanelQueue from './panels';
import { hostedElsewhere } from './placement';

/**
 * 🚨 Every host is optional. Garrison must render with none of the four widget
 * frameworks installed, and must not double up when several are.
 */
app.initializers.add('ernestdefoe-garrison', () => {
  app.routes.garrison = { path: '/garrison', component: ServersPage };

  /*
   * 🚨 `:id`, and deliberately not `:server`.
   *
   * Mithril reserves a handful of route parameter names, and a collision does
   * not error — the page simply renders nothing, which reads as a component
   * that was never finished. `id` is the safe, conventional choice and matches
   * what every core Flarum route uses.
   *
   * 🚨 This ALSO needs registering server-side, in extend.php. Without that,
   * clicking through from the list works — Mithril handles it — and loading
   * the URL directly, refreshing on it, or following the link out of an alert
   * email returns a bare 404. Which is precisely the case this page exists
   * for: the whole point of a per-server URL is that something else links to
   * it.
   */
  app.routes['garrison.server'] = { path: '/garrison/s/:id', component: ServerPage };

  /*
   * 🚨 Without this, the notification list throws `this.models[...] is not a
   * constructor` and dies — taking every other extension's notifications with
   * it. See models.js.
   */
  app.store.models['garrison-servers'] = GarrisonServer;

  /*
   * 🚨 Keyed by the blueprint's getType(). A typo here is silent: the row
   * renders empty, the unread badge still counts it, and nothing logs.
   */
  app.notificationComponents.garrisonServerIncident = ServerIncidentNotification;

  /*
   * 🚨 And the row in the user's own notification preferences.
   *
   * Registering the component makes the alert RENDER; this makes it
   * CONTROLLABLE. Without it the preference still exists — the extender
   * registered its default — but there is no checkbox anywhere that reads or
   * writes it, so somebody being paged about a server they don't run has no
   * way to stop it except asking an admin to remove their permission. A
   * setting with no control is the same failure as a control with no setting.
   */
  extend('flarum/forum/components/NotificationGrid', 'notificationTypes', function (items) {
    items.add('garrisonServerIncident', {
      name: 'garrisonServerIncident',
      icon: 'fas fa-triangle-exclamation',
      label: app.translator.trans('ernestdefoe-garrison.forum.settings.notify_garrisonServerIncident_label'),
    });
  });

  /*
   * 🚨 Pro's panels are collected BEFORE anything renders.
   *
   * garrison-pro pushes onto a queue rather than calling in, because the two
   * bundles are separate files and nothing orders them — see panels.js. This
   * drains whatever is already there and makes later pushes register straight
   * away, so it does not matter which of the two loaded first.
   */
  /*
   * 🚨 The API goes up BEFORE the queue is drained. A queued panel's view runs
   * as soon as a page renders, and the first thing most of them do is reach for
   * the shared store — publishing the surface afterwards would make that a race
   * that only loses on a fast page load.
   */
  exposeApi();
  drainPanelQueue();

  registerWidgetHosts(app, (host) => <ServerList host={host} />);

  /*
   * The stock Flarum sidebar — the floor, and the only one that needs nothing
   * else installed.
   *
   * 🚨 IndexSidebar, NOT IndexPage. The nav moved in Flarum 2 and extending
   * IndexPage.navItems silently adds nothing at all — no error, no warning,
   * just an absent link. That one cost a long "I can't find it" chase on
   * another extension.
   */
  extend('flarum/forum/components/IndexSidebar', 'items', function (items) {
    /*
     * 🚨 Asked of PLACEMENT, not of what is installed.
     *
     * The stock sidebar steps aside only when another host is actually showing
     * the list on this page — never merely because a widget framework is
     * installed. Getting that backwards is how a forum ends up with the widget
     * nowhere: Bespoke present, no Garrison block placed, sidebar suppressed
     * for a copy that does not exist. Two copies is a bug you can see; zero
     * copies is one you cannot.
     */
    if (hostedElsewhere()) return;

    items.add(
      'garrison',
      <div className="GarrisonSidebar">
        <h4 className="GarrisonSidebar-title">
          {app.translator.trans('ernestdefoe-garrison.forum.title')}

          {/*
            The heading links to the full page, matching how Calendar's own
            sidebar item behaves on this forum. A widget that shows three
            servers and offers no way to the rest is a dead end.
          */}
          <a className="GarrisonSidebar-more" href={app.route('garrison')} config={m.route.link}>
            {app.translator.trans('ernestdefoe-garrison.forum.see_all')}
          </a>
        </h4>
        <ServerList />
      </div>,
      -10
    );
  });
});
