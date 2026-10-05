/**
 * Flatten a translator result into a plain string.
 *
 * 🚨 Needed because `app.translator.trans()` returns Mithril vnodes, not text,
 * and `confirm()` stringifies an object to "[object Object]" — which is what
 * the operator would read in the dialog asking them to confirm something
 * irreversible. One copy, shared, because it was two for a while and the
 * duplicate is exactly the sort of thing that gets fixed in one place.
 */
export function extract(value) {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map(extract).join('');
  if (value && value.children) return extract(value.children);
  if (value && value.text) return value.text;

  return '';
}
