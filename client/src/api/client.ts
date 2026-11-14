import type { ApiFetch } from './wrap';
import { trackFetch } from '../utils/activity';
import { watchAnswers } from '../utils/sessionCheck'; // Fork addition

export function createApiClient(baseUrl: string = '', token: string | null = null): {
  apiFetch: ApiFetch;
  createWs: (path: string) => WebSocket;
} {
  function apiFetch(path: string, options: RequestInit = {}): Promise<Response> {
    const headers: Record<string, string> = { ...(options.headers as Record<string, string>) };
    if (token) headers['Authorization'] = `Bearer ${token}`;
    // Fork addition: report the request, so the UI can say what is running and how it ended.
    // Fork addition (watchAnswers): a 401/403 drops the kept "who am I" answer, a 429 is remembered for the notice.
    return trackFetch(baseUrl + path, options, watchAnswers(fetch(baseUrl + path, { ...options, headers })));
  }

  function createWs(path: string): WebSocket {
    let wsBase: string;
    if (baseUrl) {
      wsBase = baseUrl.replace(/^http/, 'ws');
    } else if (typeof window !== 'undefined') {
      wsBase = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') + window.location.host;
    } else {
      throw new Error('baseUrl is required for WebSocket in non-browser environments');
    }
    const url = wsBase + path + (token ? (path.includes('?') ? '&' : '?') + 'token=' + token : '');
    return new WebSocket(url);
  }

  return { apiFetch, createWs };
}
