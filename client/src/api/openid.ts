import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export interface OpenIDClient {
  id: string;
  secret: string;
  redirect: string;
}

export default function createOpenIDAPI(apiFetch: ApiFetch) {
  function list(): Promise<ApiResponse<OpenIDClient[]>> {
    return wrap(apiFetch('/cosmos/api/openid', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }))
  }

  function get(id: string): Promise<ApiResponse<OpenIDClient>> {
    return wrap(apiFetch(`/cosmos/api/openid/${encodeURIComponent(id)}`, {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }))
  }

  function create(client: OpenIDClient): Promise<ApiResponse<OpenIDClient>> {
    return wrap(apiFetch('/cosmos/api/openid', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(client),
    }))
  }

  function update(id: string, client: OpenIDClient): Promise<ApiResponse<OpenIDClient>> {
    return wrap(apiFetch(`/cosmos/api/openid/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(client),
    }))
  }

  function remove(id: string): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/openid/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }))
  }

  // Fork addition: a new secret made on the server, returned once
  function newSecret(id: string): Promise<ApiResponse<{ id: string; secret: string }>> {
    return wrap(apiFetch(`/cosmos/api/openid/${encodeURIComponent(id)}/secret`, { method: 'POST', headers: { 'Content-Type': 'application/json' } }))
  }

  return {
    newSecret,
    list,
    get,
    create,
    update,
    remove,
  };
}
