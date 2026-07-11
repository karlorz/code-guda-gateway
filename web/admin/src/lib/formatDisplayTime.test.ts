import { describe, expect, it } from 'vitest';
import { formatDisplayTime } from './formatDisplayTime';

describe('formatDisplayTime', () => {
  it('formats UTC ISO into Asia/Seoul wall time with offset label', () => {
    const out = formatDisplayTime('2026-07-12T12:00:00.000Z', 'Asia/Seoul');
    // 12:00 UTC = 21:00 KST
    expect(out).toMatch(/2026-07-12/);
    expect(out).toMatch(/21:00:00/);
    expect(out).toMatch(/GMT\+9|UTC\+9|\+09:00|KST/);
  });

  it('returns original string when parse fails', () => {
    expect(formatDisplayTime('not-a-date', 'UTC')).toBe('not-a-date');
  });

  it('formats UTC zone without shifting calendar day incorrectly', () => {
    const out = formatDisplayTime('2026-07-12T00:30:00Z', 'UTC');
    expect(out).toMatch(/2026-07-12/);
    expect(out).toMatch(/00:30:00/);
  });
});
