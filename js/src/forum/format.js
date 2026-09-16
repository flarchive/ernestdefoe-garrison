import app from 'flarum/forum/app';

/**
 * 🚨 BINARY UNITS, because that is what every one of these sources reports.
 *
 * Docker says GiB, cgroup v2 counts pages, /proc counts pages, and a backup is
 * a file on disk. Dividing by 1000 would put a number on screen that disagrees
 * with `docker stats` and `ls -lh` on the same machine — and an operator who
 * catches the panel contradicting their own terminal believes the panel
 * exactly once.
 *
 * One copy, shared. It was two for a while — the status page and the backups
 * panel each had their own — which is the shape where a rounding fix lands in
 * one of them and the two surfaces start quietly disagreeing about the size of
 * the same thing.
 */
export function bytes(n) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;

  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }

  return (i === 0 ? n : n.toFixed(n >= 10 ? 0 : 1)) + ' ' + units[i];
}

/**
 * A span of seconds, said the way a person would.
 *
 * 🚨 Rounded to the unit that matters and never more precise than that. "2h
 * 14m" is a playtime; "2 hours, 14 minutes and 6 seconds" is a stopwatch
 * reading, and nobody comparing themselves against a leaderboard cares about
 * the seconds. Below an hour, minutes are the whole answer.
 *
 * 🚨 Built from translated units rather than hardcoded letters, because "h"
 * and "m" are English abbreviations — they read as nothing in most languages,
 * and this string appears next to somebody's name on their own profile.
 */
export function duration(seconds) {
  const total = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);

  if (hours === 0) {
    return app.translator.trans('ernestdefoe-garrison.forum.duration.minutes', { count: minutes });
  }

  if (minutes === 0) {
    return app.translator.trans('ernestdefoe-garrison.forum.duration.hours', { count: hours });
  }

  return app.translator.trans('ernestdefoe-garrison.forum.duration.both', {
    hours,
    minutes,
  });
}
