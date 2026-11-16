// Fork addition (plan 44): does an app accept the address it is reached at
// (src/behind). Asking and changing are POST (administrators); the known apps
// are GET (resources-read) and stay quiet on errors.
import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export default function createUrlCheckAPI(apiFetch: ApiFetch) {
  const post = (path: string, body: object): Promise<ApiResponse> =>
    wrap(apiFetch('/cosmos/api/' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }));
  return {
    check: (route: string): Promise<ApiResponse> => post('url-check', { route }),
    checks: (routes: string[]): Promise<ApiResponse> => post('url-checks', { routes }),
    // setting: 'appOwnHost' | 'noVisitorAddress', exactly one per call
    apply: (route: string, setting: string, on: boolean): Promise<ApiResponse> => post('url-check/apply', { route, [setting]: on }),
    knownApps: (): Promise<ApiResponse> => wrap(apiFetch('/cosmos/api/known-apps', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }), true),
  };
}
