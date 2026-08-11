import React, { useState, useEffect, useCallback, useRef } from 'react';
import type { StateResponse, ItemView, RunResponse, ItemResponse, LogView } from './types/api';
import { api } from './api/client';
import { TopBar } from './components/TopBar/TopBar';
import { ActiveAgent } from './components/ActiveAgent/ActiveAgent';
import { KanbanBoard } from './components/Kanban/KanbanBoard';
import { ItemDetailModal } from './components/ItemDetail/ItemDetailModal';
import { LogViewer } from './components/LogViewer/LogViewer';
import { AlertCircle } from 'lucide-react';

export const App: React.FC = () => {
  const [state, setState] = useState<StateResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isRefreshing, setIsRefreshing] = useState(false);

  // ルーティング状態（ハッシュからパース）
  const [route, setRoute] = useState<string>(window.location.hash || '#/');

  // Issue詳細モーダル用
  const [selectedItem, setSelectedItem] = useState<ItemView | null>(null);
  const [itemRuns, setItemRuns] = useState<ItemResponse['runs']>([]);
  const [isLoadingRuns, setIsLoadingRuns] = useState(false);

  // ログビューア用
  const [selectedRunId, setSelectedRunId] = useState<number | null>(null);
  const [runDetail, setRunDetail] = useState<RunResponse | null>(null);
  const [isStreamingLog, setIsStreamingLog] = useState(false);
  const logOffsetRef = useRef<number>(0);

  // ハッシュ変更の監視
  useEffect(() => {
    const handleHashChange = () => {
      setRoute(window.location.hash || '#/');
    };
    window.addEventListener('hashchange', handleHashChange);
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, []);

  // state の取得
  const fetchState = useCallback(async (showIndicator = false) => {
    if (showIndicator) setIsRefreshing(true);
    try {
      const data = await api.getState();
      setState(data);
      setError(null);
    } catch (err: unknown) {
      if (err instanceof Error) {
        setError(err.message);
      } else {
        setError('状態の取得に失敗しました');
      }
    } finally {
      if (showIndicator) setIsRefreshing(false);
    }
  }, []);

  // 初回取得 & ポーリング (2秒間隔)
  useEffect(() => {
    fetchState();
    const interval = setInterval(() => fetchState(false), 2000);
    return () => clearInterval(interval);
  }, [fetchState]);

  // ルートの解析とデータ同期
  useEffect(() => {
    const hash = route.replace(/^#/, '');

    // #/run/:id
    if (hash.startsWith('/run/')) {
      const runId = Number(hash.split('/')[2]);
      if (!isNaN(runId)) {
        setSelectedRunId(runId);
        setSelectedItem(null);
        return;
      }
    }

    // #/item/:repo/:issue
    if (hash.startsWith('/item/')) {
      const parts = hash.split('/');
      const repo = decodeURIComponent(parts[2] || '');
      const issue = Number(parts[3]);
      if (repo && !isNaN(issue)) {
        setSelectedRunId(null);
        // state から item を探す
        if (state) {
          const found = state.items.find((it) => it.repo === repo && it.issue === issue);
          if (found) {
            setSelectedItem(found);
          }
        }
        return;
      }
    }

    // #/ (ダッシュボード)
    setSelectedRunId(null);
    setSelectedItem(null);
  }, [route, state]);

  // Item詳細が開かれたときの Run 履歴取得
  useEffect(() => {
    if (!selectedItem) {
      setItemRuns([]);
      return;
    }
    let isCancelled = false;
    setIsLoadingRuns(true);
    api
      .getItem(selectedItem.repo, selectedItem.issue)
      .then((res) => {
        if (!isCancelled) {
          setItemRuns(res.runs);
        }
      })
      .catch((err) => {
        console.error('Failed to fetch item runs:', err);
      })
      .finally(() => {
        if (!isCancelled) setIsLoadingRuns(false);
      });

    return () => {
      isCancelled = true;
    };
  }, [selectedItem]);

  // Run詳細の取得とリアルタイムログストリーミング追従
  useEffect(() => {
    if (!selectedRunId) {
      setRunDetail(null);
      setIsStreamingLog(false);
      return;
    }

    let isCancelled = false;
    let pollTimer: ReturnType<typeof setTimeout> | null = null;

    // 初回の完全な Run データ取得
    api
      .getRun(selectedRunId)
      .then((res) => {
        if (isCancelled) return;
        setRunDetail(res);
        logOffsetRef.current = res.log?.size || 0;

        // 実行中の場合は差分ストリーミングを開始
        if (res.run.running) {
          setIsStreamingLog(true);
          const pollLog = async () => {
            if (isCancelled) return;
            try {
              const chunk = await api.getRunLogChunk(selectedRunId, logOffsetRef.current);
              if (isCancelled) return;

              if (chunk.data) {
                setRunDetail((prev) => {
                  if (!prev) return prev;
                  const currentLog: LogView = prev.log || {
                    header: '',
                    prompt: '',
                    prompt_truncated: false,
                    output: '',
                    output_truncated: false,
                    size: 0,
                  };
                  return {
                    ...prev,
                    log: {
                      ...currentLog,
                      output: currentLog.output + chunk.data,
                      size: chunk.size,
                    },
                  };
                });
                logOffsetRef.current = chunk.next;
              }

              if (chunk.running) {
                pollTimer = setTimeout(pollLog, 1500);
              } else {
                setIsStreamingLog(false);
                // 終了した場合は最終状態を再取得
                const finalRun = await api.getRun(selectedRunId);
                if (!isCancelled) setRunDetail(finalRun);
              }
            } catch (err) {
              console.error('Log chunk polling error:', err);
              if (!isCancelled) {
                pollTimer = setTimeout(pollLog, 3000);
              }
            }
          };

          pollTimer = setTimeout(pollLog, 1500);
        }
      })
      .catch((err) => {
        if (!isCancelled) {
          console.error('Failed to get run detail:', err);
        }
      });

    return () => {
      isCancelled = true;
      if (pollTimer) clearTimeout(pollTimer);
    };
  }, [selectedRunId]);

  const handleSelectItem = (item: ItemView) => {
    window.location.hash = `#/item/${encodeURIComponent(item.repo)}/${item.issue}`;
  };

  const handleSelectRun = (runId: number) => {
    window.location.hash = `#/run/${runId}`;
  };

  const handleBackToDashboard = () => {
    window.location.hash = '#/';
  };

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100 flex flex-col font-sans">
      <TopBar
        meta={state?.meta}
        generatedAt={state?.generated_at}
        isRefreshing={isRefreshing}
        onRefresh={() => fetchState(true)}
      />

      <main className="flex-1 p-4 md:p-6 max-w-7xl mx-auto w-full">
        {error && (
          <div className="mb-4 p-4 rounded-xl bg-rose-950/40 border border-rose-800/80 text-rose-300 text-xs flex items-center gap-2 shadow-lg">
            <AlertCircle className="w-4 h-4 text-rose-400 shrink-0" />
            <span>サーバとの通信に失敗しました: {error}</span>
          </div>
        )}

        {/* ログビューア表示時 */}
        {selectedRunId && runDetail ? (
          <LogViewer
            run={runDetail.run}
            log={runDetail.log}
            logError={runDetail.log_error}
            isStreaming={isStreamingLog}
            onBack={handleBackToDashboard}
          />
        ) : (
          /* ダッシュボード (カンバン & ActiveAgent) */
          <div className="space-y-6">
            <ActiveAgent
              active={state?.active}
              queueDepth={state?.queue_depth ?? 0}
              activeHasLog={state?.active_has_log}
              onSelectRun={handleSelectRun}
              onSelectIssue={(repo, issue) => {
                window.location.hash = `#/item/${encodeURIComponent(repo)}/${issue}`;
              }}
            />

            {state ? (
              <KanbanBoard
                statuses={state.meta.statuses}
                items={state.items}
                onSelectItem={handleSelectItem}
                onSelectRun={handleSelectRun}
              />
            ) : (
              <div className="py-20 text-center text-slate-500 font-mono text-xs">
                カンバンデータを読み込み中…
              </div>
            )}
          </div>
        )}

        {/* Item詳細モーダル */}
        {selectedItem && (
          <ItemDetailModal
            item={selectedItem}
            runs={itemRuns}
            isLoadingRuns={isLoadingRuns}
            onClose={handleBackToDashboard}
            onSelectRun={handleSelectRun}
          />
        )}
      </main>
    </div>
  );
};
