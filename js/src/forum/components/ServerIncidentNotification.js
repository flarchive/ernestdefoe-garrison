import app from 'flarum/forum/app';
import Notification from 'flarum/forum/components/Notification';

/**
 * "Bare-metal stand-in stopped accepting players."
 *
 * 🚨 REGISTERED IN app.notificationComponents OR IT RENDERS AS AN EMPTY ROW.
 *
 * Flarum looks the component up by the blueprint's type string. A missing
 * entry is not an error anywhere: the notification list draws a blank line,
 * the unread count still goes up, and the only symptom is a dropdown with a
 * gap in it. That is the worst possible outcome for this particular feature —
 * the alert did arrive, it just says nothing, so the operator learns to
 * ignore it.
 */
export default class ServerIncidentNotification extends Notification {
  icon() {
    const state = this.data().state;

    /*
     * The icon carries the severity, because in a dropdown of twenty rows the
     * icon is read before the words. Recovery gets its own, and deliberately
     * not a warning colour — "it came back" should not look like "it broke".
     */
    if (state === 'recovered') return 'fas fa-heart-pulse';
    if (state === 'abandoned') return 'fas fa-hand';

    return 'fas fa-triangle-exclamation';
  }

  /**
   * 🚨 Straight to the server the alert is about, not to the list.
   *
   * An alert that says "Shattered Pact stopped accepting players" and lands
   * somebody on a page of eleven servers has handed them a search task at the
   * moment they are least able to do one. The subject id is the server's — it
   * is what the blueprint reports as its subject — so the link is exact.
   *
   * Falls back to the list if the subject did not come through, which happens
   * for a notification whose server has since been deleted. A dead link would
   * be the worse answer there: the list at least explains itself.
   */
  href() {
    const subject = this.attrs.notification.subject();

    return subject
      ? app.route('garrison.server', { id: subject.id() })
      : app.route('garrison');
  }

  content() {
    const data = this.data();

    return app.translator.trans(`ernestdefoe-garrison.forum.notification.${this.key(data.state)}`, {
      name: data.name,
    });
  }

  excerpt() {
    // The probe's own words — "UDP 2457 has 9600 bytes queued" — rather than a
    // restatement of the headline. An operator opening this at 3am needs the
    // finding, and it is the one thing the notification knows that the status
    // page would make them hunt for.
    return this.data().summary || null;
  }

  /**
   * 🚨 A closed set, mapped explicitly, rather than interpolating the state
   * straight into a translation key. An unexpected state would otherwise ask
   * the translator for a key that does not exist and render its raw name —
   * `ernestdefoe-garrison.forum.notification.undefined` — in somebody's
   * notification list.
   */
  key(state) {
    switch (state) {
      case 'recovered':
        return 'recovered';
      case 'abandoned':
        return 'abandoned';
      case 'unready':
        return 'unready';
      default:
        return 'down';
    }
  }

  data() {
    return this.attrs.notification.content() || {};
  }
}
