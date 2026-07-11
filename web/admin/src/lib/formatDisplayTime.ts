/** Format a UTC RFC3339/ISO timestamp for admin display in a gateway IANA zone. */
export function formatDisplayTime(isoUtc: string, timeZone: string): string {
  if (!isoUtc) return '';
  const d = new Date(isoUtc);
  if (Number.isNaN(d.getTime())) return isoUtc;
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: timeZone || 'UTC',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZoneName: 'shortOffset',
    }).formatToParts(d);
    const pick = (type: Intl.DateTimeFormatPartTypes) =>
      parts.find((p) => p.type === type)?.value ?? '';
    const y = pick('year');
    const m = pick('month');
    const day = pick('day');
    const h = pick('hour');
    const min = pick('minute');
    const s = pick('second');
    const tz = pick('timeZoneName') || timeZone;
    return `${y}-${m}-${day} ${h}:${min}:${s} ${tz}`;
  } catch {
    return isoUtc;
  }
}
