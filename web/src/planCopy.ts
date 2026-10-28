/**
 * One short line under each plan's name: who the plan is for and what really differs (capacity, check speed, history), in relative words with no numbers.
 * The plan cards show the real limits from the plans table, and a number here would go stale when a limit changes. It never mentions features that are not
 * built (SMS, priority support); the landing page tests guard that. Team seats are built, but they are a number, so they appear in the plan card's list from the
 * plans table and not here. A plan code that is not listed here simply shows no line.
 */
const TAGLINES: Record<string, string> = {
  free: 'Everything you need to watch a personal site or a small project.',
  pro: 'Faster checks and deeper history for growing websites.',
  agency: 'Room for many client websites, with the longest history.',
}

export function planTagline(code: string): string | undefined {
  return TAGLINES[code]
}
