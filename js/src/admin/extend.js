import app from 'flarum/admin/app';
import Extend from 'flarum/common/extenders';

import GarrisonPage from './components/GarrisonPage';

/**
 * 🚨 The Admin extender, NOT `app.extensionData`.
 *
 * `app.extensionData` does not exist in Flarum 2. Reaching for `.for()` on it
 * throws inside the initializer — and an initializer that throws does not
 * merely fail itself, it takes every extension registered after it down too.
 *
 * What that looks like from the outside is the reason this went unnoticed:
 * Garrison's whole admin screen — pairing hosts, the webhook, per-server
 * settings, logos — compiled into the bundle, shipped, and was never reached,
 * while core's fallback said "This extension has no settings". A page that is
 * never registered and a page that was never written read exactly the same.
 * Everything on it had been exercised through the API and the console, so
 * nothing else complained.
 *
 * 🚨 Every `.permission()` takes a CALLBACK, not an object.
 *
 * This array is built when the module is evaluated — while the admin bundle is
 * still executing, long before the app has booted. An object literal would
 * call `app.translator.trans()` right there; the translator reaches into
 * `app.data.resources`, `app.data` is undefined, and the module throws with
 * the same invisible consequence. Core's own extensions all wrap these in
 * `() => ({ … })`.
 *
 * Both traps are recorded from Atrium, which hit them in the same order.
 */
export default [
  new Extend.Admin()
    .page(GarrisonPage)

    /*
     * 🚨 Permission labels say what each one ACTUALLY does. "View game
     * servers" would be a lie: a server marked public is visible to everyone
     * with no permission at all, and an admin who grants this expecting it to
     * control that will be confused for a long time.
     */
    .permission(
      () => ({
        icon: 'fas fa-eye',
        label: app.translator.trans('ernestdefoe-garrison.admin.permissions.view'),
        permission: 'garrison.view',
      }),
      'view'
    )
    .permission(
      () => ({
        icon: 'fas fa-power-off',
        label: app.translator.trans('ernestdefoe-garrison.admin.permissions.control'),
        permission: 'garrison.control',
      }),
      'moderate'
    )
    .permission(
      () => ({
        icon: 'fas fa-terminal',
        label: app.translator.trans('ernestdefoe-garrison.admin.permissions.console'),
        permission: 'garrison.console',
      }),
      'moderate'
    )
    .permission(
      () => ({
        icon: 'fas fa-sliders',
        label: app.translator.trans('ernestdefoe-garrison.admin.permissions.config'),
        permission: 'garrison.config',
      }),
      'moderate'
    )
    .permission(
      () => ({
        icon: 'fas fa-tower-observation',
        label: app.translator.trans('ernestdefoe-garrison.admin.permissions.manage'),
        permission: 'garrison.manage',
      }),
      'moderate'
    ),
];
