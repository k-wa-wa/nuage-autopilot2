import { http, HttpResponse } from 'msw';
import {
  mockStateActive,
  mockStateIdle,
  mockItemDetail,
  mockRunDetail,
  mockLogChunk,
} from './fixtures';

export const handlers = [
  http.get('/api/state', ({ request }) => {
    const url = new URL(request.url);
    if (url.searchParams.get('mode') === 'idle') {
      return HttpResponse.json(mockStateIdle);
    }
    return HttpResponse.json(mockStateActive);
  }),

  http.get('/api/item', ({ request }) => {
    const url = new URL(request.url);
    const repo = url.searchParams.get('repo');
    const issue = Number(url.searchParams.get('issue'));

    if (!repo || isNaN(issue)) {
      return HttpResponse.json({ error: 'repo と issue を指定してください' }, { status: 400 });
    }

    return HttpResponse.json(mockItemDetail);
  }),

  http.get('/api/run', ({ request }) => {
    const url = new URL(request.url);
    const id = url.searchParams.get('id');
    if (!id) {
      return HttpResponse.json({ error: 'id を指定してください' }, { status: 400 });
    }
    return HttpResponse.json(mockRunDetail);
  }),

  http.get('/api/run/log', ({ request }) => {
    const url = new URL(request.url);
    const id = url.searchParams.get('id');
    if (!id) {
      return HttpResponse.json({ error: 'id を指定してください' }, { status: 400 });
    }
    return HttpResponse.json(mockLogChunk);
  }),
];
