import type { StateResponse, ItemResponse, RunResponse, LogChunkResponse } from '../types/api';

// 接続先サーバのベース URL (未指定時は同一オリジン / 相対パス)
function getBaseUrl(): string {
  const urlParams = typeof window !== 'undefined' ? new URLSearchParams(window.location.search) : null;
  const customApi = urlParams?.get('api');
  if (customApi) return customApi.replace(/\/$/, '');
  return (import.meta.env.VITE_API_URL || '').replace(/\/$/, '');
}

async function fetchJSON<T>(path: string): Promise<T> {
  const baseUrl = getBaseUrl();
  const url = `${baseUrl}${path}`;
  const res = await fetch(url, {
    headers: {
      Accept: 'application/json',
    },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  return data as T;
}

export const api = {
  getState: (): Promise<StateResponse> => fetchJSON<StateResponse>('/api/state'),

  getItem: (repo: string, issue: number): Promise<ItemResponse> =>
    fetchJSON<ItemResponse>(`/api/item?repo=${encodeURIComponent(repo)}&issue=${issue}`),

  getRun: (id: number): Promise<RunResponse> =>
    fetchJSON<RunResponse>(`/api/run?id=${id}`),

  getRunLogChunk: (id: number, offset: number): Promise<LogChunkResponse> =>
    fetchJSON<LogChunkResponse>(`/api/run/log?id=${id}&offset=${offset}`),
};
