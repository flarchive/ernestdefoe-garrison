import Model from 'flarum/common/Model';

/**
 * The client-side store's idea of a server.
 *
 * 🚨 THIS EXISTS FOR NOTIFICATIONS, AND REGISTERING IT IS NOT OPTIONAL.
 *
 * `/api/notifications` includes each notification's subject, and Flarum's store
 * does `new this.models[type](data, this)` with no fallback of any kind. An
 * unregistered type is not a generic record — it is
 *
 *     TypeError: this.models[type] is not a constructor
 *
 * thrown while the notification list is being built, which kills the WHOLE
 * list: every notification on it, including ones from other extensions and
 * from core. One missing line here breaks a part of the forum that has nothing
 * to do with game servers.
 *
 * It is the same trap as the missing `Extend\ApiResource` on the PHP side, one
 * layer up, and it fails just as far from its cause. Both halves are needed:
 * the resource makes the subject serializable, this makes it constructible.
 *
 * 🚨 Garrison's own UI does NOT read servers through the store — the status
 * page and every widget read the plain-JSON list endpoint, which shapes each
 * row per-actor. So the attributes here are only the ones the JSON:API
 * resource actually publishes, and nothing in this file should grow to mirror
 * that endpoint: two models of the same thing is how two surfaces end up
 * disagreeing about what a server is.
 */
export class GarrisonServer extends Model {}

Object.assign(GarrisonServer.prototype, {
  name: Model.attribute('name'),
  ref: Model.attribute('ref'),
  game: Model.attribute('game'),
  state: Model.attribute('state'),
  health: Model.attribute('health'),
  iconUrl: Model.attribute('iconUrl'),
  isPublic: Model.attribute('isPublic'),
});
