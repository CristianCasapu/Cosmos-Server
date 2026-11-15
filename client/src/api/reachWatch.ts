// Fork addition: the watch of the server's reachability from the internet
// (src/reachwatch). Reading is GET and stays quiet on errors; testing now and
// the settings are POST.
import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export default function createReachWatchAPI(apiFetch: ApiFetch) {
  const post = (path: string, body: object): Promise<ApiResponse> =>
    wrap(apiFetch('/cosmos/api/reach-watch/' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }));
  return {
    status: (): Promise<ApiResponse> => wrap(apiFetch('/cosmos/api/reach-watch', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }), true),
    check: (): Promise<ApiResponse> => post('check', {}),
    settings: (body: { enabled?: boolean; minutes?: number }): Promise<ApiResponse> => post('settings', body),
  };
}
