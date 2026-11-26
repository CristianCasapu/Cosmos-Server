// Fork addition: the notifications page, its details and the bell
// (src/fork_notifications.go). Reading is GET; marking and deleting are POST.
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

export default function createNotificationsAPI(apiFetch: ApiFetch) {
  const get = (path: string, params: Params = {}, noError = false): Promise<ApiResponse> =>
    wrap(apiFetch('/cosmos/api/notifications/' + path + query(params), {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }), noError);
  const post = (path: string, body: object): Promise<ApiResponse> =>
    wrap(apiFetch('/cosmos/api/notifications/' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }));

  return {
    // level, source, read, q, cursor, limit
    list: (params: Params, quiet = false) => get('list', params, quiet),
    item: (params: { id?: number; ref?: string }) => get('item', params as Params, true),
    // admins only: answers 403 to anyone else, without a message
    context: (id: number) => apiFetch('/cosmos/api/notifications/context' + query({ id }), {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }).then((response) => {
      if (response.status === 401 || response.status === 403) {
        const refused: Error & { refused?: boolean } = new Error('refused');
        refused.refused = true;
        throw refused;
      }
      return wrap(Promise.resolve(response), true);
    }),
    mark: (body: object) => post('mark', body),
    remove: (body: object) => post('delete', body),
    // plan 48, review 7: the owner's word that the problem is over
    resolve: (id: number) => post('resolve', { ids: [id] }),
  };
}
