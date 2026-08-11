import type { StateResponse, ItemResponse, RunResponse, LogChunkResponse } from '../types/api';

async function fetchJSON<T>(url: string): Promise<T> {
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
