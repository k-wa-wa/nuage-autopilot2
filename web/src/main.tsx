import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import './index.css';

async function enableMocking() {
  const urlParams = new URLSearchParams(window.location.search);
  const hasCustomApi = urlParams.has('api') || Boolean(import.meta.env.VITE_API_URL);
  const isMockDisabled = urlParams.get('mock') === 'false';

  // 基本はローカル開発 (DEV) でモック ON。IP/API が指定された時や明示的 OFF の時だけ無効化
  const isMock = import.meta.env.DEV && !hasCustomApi && !isMockDisabled;

  if (isMock) {
    const { setupWorker } = await import('msw/browser');
    const { handlers } = await import('./mocks/handlers');
    const worker = setupWorker(...handlers);
    await worker.start({
      onUnhandledRequest: 'bypass',
    });
    console.log('[MSW] Mocking enabled (デフォルトモックON)');
  }
}

enableMocking().then(() => {
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  );
});
