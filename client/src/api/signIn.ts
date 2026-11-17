// Fork addition (plan 45): the sign-in details of apps (src/appsignin). The
// defaults and the details are GET; a secret is asked for with a POST, one at
// a time, and is never kept by this module.
import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export default function createSignInAPI(apiFetch: ApiFetch) {
  const json = { 'Content-Type': 'application/json' };
  const path = (name: string) => '/cosmos/api/servapps/' + encodeURIComponent(name) + '/sign-in';
  return {
    defaults: (): Promise<ApiResponse> => wrap(apiFetch('/cosmos/api/app-sign-in/defaults', { method: 'GET', headers: json }), true),
    details: (name: string): Promise<ApiResponse> => wrap(apiFetch(path(name), { method: 'GET', headers: json }), true),
    reveal: (name: string, secret: string, purpose: 'show' | 'copy'): Promise<ApiResponse> => wrap(apiFetch(path(name) + '/reveal', {
      method: 'POST',
      headers: json,
      body: JSON.stringify({ secret, purpose }),
    }), true),
  };
}
