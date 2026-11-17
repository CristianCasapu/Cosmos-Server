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
  // the server reloads its URLs after a change, which ends the connections for
  // a moment: a request that met that moment is sent again (each is idempotent)
  const retried = async (path: string, body: object): Promise<ApiResponse> => {
    for (let i = 0; ; i++) {
      try {
        return await post(path, body);
      } catch (e) {
        if (i >= 3 || !(e instanceof TypeError)) throw e;
        await new Promise((r) => setTimeout(r, 1500));
      }
    }
  };
  return {
    check: (route: string): Promise<ApiResponse> => retried('url-check', { route }),
    checks: (routes: string[]): Promise<ApiResponse> => post('url-checks', { routes }),
    // setting: 'appOwnHost' | 'noVisitorAddress', exactly one per call
    // more: another URL of the same repair follows at once (the server then reloads its URLs once, after the last)
    apply: (route: string, setting: string, on: boolean, more: boolean = false): Promise<ApiResponse> => retried('url-check/apply', { route, [setting]: on, more }),
    knownApps: (): Promise<ApiResponse> => wrap(apiFetch('/cosmos/api/known-apps', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }), true),
  };
}
