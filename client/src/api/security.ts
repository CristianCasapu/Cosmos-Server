// Fork addition: the security record's read API, for the security dashboard
// (pages/security). Every route is GET and needs the Admin Read permission.
import wrap, { type ApiResponse, type ApiFetch } from './wrap';

type Params = Record<string, string | number | undefined | null>;

const query = (params: Params) => {
  const search = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') search.set(key, String(value));
  });
  const text = search.toString();
  return text ? '?' + text : '';
};

export default function createSecurityAPI(apiFetch: ApiFetch) {
  const get = (path: string, params: Params = {}, noError = false): Promise<ApiResponse> =>
    wrap(apiFetch('/cosmos/api/security/' + path + query(params), {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }), noError);

  return {
    // period: 24h, 7d or 30d; tz: minutes east of UTC
    overview: (period: string, tz: number) => get('overview', { period, tz }),
    // group, severity, ip, q, cursor, limit
    incidents: (params: Params) => get('incidents', params),
    kinds: () => get('kinds', {}, true),
  };
}
