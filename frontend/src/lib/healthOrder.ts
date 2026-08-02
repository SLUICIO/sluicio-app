// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Ordering dashboard cards by how much they need looking at.
//
// A dashboard is read top-left first, so whatever is broken belongs
// there. Integrations used to be ordered by traffic volume and systems
// by the order they happened to be pinned, which means the one thing
// that is on fire could sit below a screenful of healthy, chatty cards.
//
// Rank on the STATUS STRING rather than the rendered pip: the two card
// kinds map status to a pip differently ("errors" is a red pip for an
// integration and an amber one for a system), so ranking on the pip
// would order the two kinds inconsistently on a board holding both.
//
// Name is the tiebreak, so a board with nothing wrong is stable and
// alphabetical rather than shuffling as traffic moves around.

/** Worst first; idle and unknown last. Lower sorts earlier. */
export function healthRank(status: string | undefined): number {
  switch (status) {
    case "unhealthy":
      return 0;
    case "errors":
      return 1;
    case "ok":
      return 2;
    // "quiet" means no traffic in the window — fine, not a problem, but
    // nothing to look at either, so it sits below anything live.
    case "quiet":
      return 3;
    default:
      return 4;
  }
}

/**
 * Comparator for anything carrying a status and a name.
 *
 * Names compare with localeCompare so a board is ordered the way a
 * reader expects rather than by code point — "Åre" belongs after "Alpha",
 * not after "Zulu".
 */
export function byHealthThenName<T>(
  status: (x: T) => string | undefined,
  name: (x: T) => string,
): (a: T, b: T) => number {
  return (a, b) => {
    const d = healthRank(status(a)) - healthRank(status(b));
    if (d !== 0) return d;
    return name(a).localeCompare(name(b));
  };
}
