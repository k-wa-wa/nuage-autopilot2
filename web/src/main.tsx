import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import './index.css';

async function enableMocking() {
  const urlParams = new URLSearchParams(window.location.search);
  const isMock = urlParams.has('mock') || import.meta.env.VITE_ENABLE_MOCK === 'true';

  if (isMock) {
    const { setupWorker } = await import('msw/browser');
    const { handlers } = await import('./mocks/handlers');
    const worker = setupWorker(...handlers);
    await worker.start({
      onUnhandledRequest: 'bypass',
    });
    console.log('[MSW] Mocking enabled');
  }
}

enableMocking().then(() => {
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  );
});
