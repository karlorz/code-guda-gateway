import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '../api/client';
import type { DisplayTimezoneSetting } from '../api/types';

export const displayTimezoneQueryKey = ['display-timezone'] as const;

/** Shared admin display timezone from gateway settings (not browser locale). */
export function useDisplayTimezone() {
  const query = useQuery({
    queryKey: displayTimezoneQueryKey,
    queryFn: () => apiFetch<DisplayTimezoneSetting>('/admin/api/settings/display-timezone'),
  });
  return {
    ...query,
    zone: query.data?.timezone || 'UTC',
  };
}
