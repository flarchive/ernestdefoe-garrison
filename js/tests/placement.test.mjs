/**
 * Placement accounting — the thing that decides whether the stock sidebar
 * shows the server list.
 *
 * 🚨 Every bug this can have is INVISIBLE in a browser. Over-count and the
 * sidebar goes quiet for ever; under-count and the list appears twice; get the
 * SIDEBAR exemption wrong and the sidebar silences itself. None of the three
 * throws, logs, or looks like anything but a layout the operator chose.
 *
 * No test runner: `node --test`, same as Scribe.
 */

import test from 'node:test';
import assert from 'node:assert/strict';

// The module calls m.redraw() on transitions. In a browser that global is
// Mithril; here it is a counter, so the redraw contract is testable too.
let redraws = 0;
globalThis.m = { redraw: () => redraws++ };

const { claim, release, hostedElsewhere, SIDEBAR } = await import('../src/forum/placement.js');

/** placement.js keeps module state, so every test starts from a clean slate. */
function reset(hosts) {
  for (const h of hosts) release(h);
  redraws = 0;
}

test('nothing placed means the sidebar renders', () => {
  assert.equal(hostedElsewhere(), false);
});

test('the sidebar does not count as somebody else', () => {
  claim(SIDEBAR);
  assert.equal(hostedElsewhere(), false, 'the sidebar would silence itself');
  reset([SIDEBAR]);
});

test('a host placing the widget silences the sidebar, and releasing brings it back', () => {
  claim('bespoke');
  assert.equal(hostedElsewhere(), true);

  release('bespoke');
  assert.equal(hostedElsewhere(), false, 'removing the block must restore the sidebar');
});

test('two blocks from one host: the sidebar returns only when BOTH are gone', () => {
  claim('page-builder');
  claim('page-builder');

  release('page-builder');
  assert.equal(hostedElsewhere(), true, 'one block is still on the page');

  release('page-builder');
  assert.equal(hostedElsewhere(), false);
});

test('hosts are counted independently', () => {
  claim('bespoke');
  claim('fof');

  release('bespoke');
  assert.equal(hostedElsewhere(), true, 'fof is still showing it');

  release('fof');
  assert.equal(hostedElsewhere(), false);
});

test('an unmatched release cannot drive the count negative', () => {
  /*
   * 🚨 The failure this guards. If release() let the count go to -1, the next
   * claim() would bring it to 0 — a placed widget that reads as absent, so the
   * list renders twice for ever with nothing to explain it.
   */
  release('bespoke');
  release('bespoke');

  claim('bespoke');
  assert.equal(hostedElsewhere(), true, 'a real placement must still register');

  release('bespoke');
  assert.equal(hostedElsewhere(), false);
});

test('redraw fires on the transitions, and only on the transitions', () => {
  redraws = 0;

  claim('bespoke');
  assert.equal(redraws, 1, 'the sidebar must be told to stand down');

  claim('bespoke');
  assert.equal(redraws, 1, 'a second block changes no answer');

  release('bespoke');
  assert.equal(redraws, 1, 'one block remains; still hosted');

  release('bespoke');
  assert.equal(redraws, 2, 'the sidebar must be told to come back');
});

test('the sidebar never schedules a redraw', () => {
  redraws = 0;

  claim(SIDEBAR);
  release(SIDEBAR);

  /*
   * 🚨 Because claim() runs in oncreate — inside the draw. A redraw requested
   * from there by the very component being drawn is a draw that schedules a
   * draw, on every page load, for ever.
   */
  assert.equal(redraws, 0);
});
