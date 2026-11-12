import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export default function createShieldAPI(apiFetch: ApiFetch) {
  function bans(): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/shield/bans', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function unban(clientID: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/shield/unban', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ clientID }),
    }))
  }

  return {
    bans,
    unban,
  };
}
