// Fork addition (plan 48): every kind of Telegram message as the server
// renders it with sample values, and "send me this sample". Reading is GET
// and stays quiet on errors; sending is POST.
import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export default function createTelegramMessagesAPI(apiFetch: ApiFetch) {
  return {
    kinds: (): Promise<ApiResponse> => wrap(apiFetch('/cosmos/api/telegram/kinds', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }), true),
    sample: (kind: string): Promise<ApiResponse> => wrap(apiFetch('/cosmos/api/telegram/sample', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kind }),
    })),
  };
}
