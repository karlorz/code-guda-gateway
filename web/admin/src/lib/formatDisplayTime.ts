/** Format a UTC RFC3339/ISO timestamp for admin display in a gateway IANA zone. */

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatterFor(timeZone: string): Intl.DateTimeFormat {
  const zone = timeZone || 'UTC';
  let fmt = formatters.get(zone);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat('en-CA', {
      timeZone: zone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZoneName: 'shortOffset',
    });
    formatters.set(zone, fmt);
  }
  return fmt;
}

export function formatDisplayTime(isoUtc: string, timeZone: string): string {
  if (!isoUtc) return '';
  const d = new Date(isoUtc);
  if (Number.isNaN(d.getTime())) return isoUtc;
  try {
    const parts = formatterFor(timeZone).formatToParts(d);
    const byType: Partial<Record<Intl.DateTimeFormatPartTypes, string>> = {};
    for (const p of parts) {
      byType[p.type] = p.value;
    }
    const y = byType.year ?? '';
    const m = byType.month ?? '';
    const day = byType.day ?? '';
    const h = byType.hour ?? '';
    const min = byType.minute ?? '';
    const s = byType.second ?? '';
    const tz = byType.timeZoneName || timeZone || 'UTC';
    return `${y}-${m}-${day} ${h}:${min}:${s} ${tz}`;
  } catch {
    return isoUtc;
  }
}
