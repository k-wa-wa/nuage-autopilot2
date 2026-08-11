import React, { useState, useEffect, useCallback, useRef } from 'react';
import type { StateResponse, ItemView, RunResponse, ItemResponse, LogView } from './types/api';
import { api } from './api/client';
import { TopBar } from './components/TopBar/TopBar';
import { ActiveAgent } from './components/ActiveAgent/ActiveAgent';
import { LaneList } from './components/LaneList/LaneList';
import { ItemDetailModal } from './components/ItemDetail/ItemDetailModal';
import { LogViewer } from './components/LogViewer/LogViewer';
import { AlertCircle } from 'lucide-react';

export const App: React.FC = () => {
  const [state, setState] = useState<StateResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isRefreshing, setIsRefreshing] = useState(false);

  // ルーティング状態（ハッシュからパース）
  const [route, setRoute] = useState<string>(window.location.hash || '#/');

  // Issue詳細モーダル用 (repo と issue でターゲットを特定)
  const [targetItemKey, setTargetItemKey] = useState<{ repo: string; issue: number } | null>(null);
  const [itemDetail, setItemDetail] = useState<ItemResponse | null>(null);
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

  // ルートの解析
  useEffect(() => {
    const hash = route.replace(/^#/, '');

    // #/run/:id
    if (hash.startsWith('/run/')) {
      const runId = Number(hash.split('/')[2]);
      if (!isNaN(runId)) {
        setSelectedRunId(runId);
        setTargetItemKey(null);
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
        setTargetItemKey((prev) => {
          if (prev?.repo === repo && prev?.issue === issue) return prev;
          return { repo, issue };
        });
        return;
      }
    }

    // #/ (ダッシュボード)
    setSelectedRunId(null);
    setTargetItemKey(null);
  }, [route]);

  // Item詳細が開かれたときのデータ取得 (初回のみローディング表示、以降はサイレント更新)
  useEffect(() => {
    if (!targetItemKey) {
      setItemDetail(null);
      return;
    }

    const { repo, issue } = targetItemKey;
    let isCancelled = false;

    // 初回のみローディング表示
    setIsLoadingRuns(true);

    const loadItem = async (isFirst: boolean) => {
      try {
        const res = await api.getItem(repo, issue);
        if (!isCancelled) {
          setItemDetail(res);
        }
      } catch (err) {
        if (!isCancelled && isFirst) {
          console.error('Failed to fetch item detail:', err);
        }
      } finally {
        if (!isCancelled && isFirst) {
          setIsLoadingRuns(false);
        }
      }
    };

    loadItem(true);

    // モーダルが開いている間は 3 秒ごとにサイレント更新
    const interval = setInterval(() => loadItem(false), 3000);

    return () => {
      isCancelled = true;
      clearInterval(interval);
    };
  }, [targetItemKey]);

  // Run詳細の取得とリアルタイムログストリーミング追従
  useEffect(() => {
    if (!selectedRunId) {
      setRunDetail(null);
      setIsStreamingLog(false);
      return;
    }

    let isCancelled = false;
    let pollTimer: ReturnType<typeof setTimeout> | null = null;

    api
      .getRun(selectedRunId)
      .then((res) => {
        if (isCancelled) return;
        setRunDetail(res);
        logOffsetRef.current = res.log?.size || 0;

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
    <div className="min-h-screen bg-[#0d1117] text-[#c9d1d9] flex flex-col font-sans selection:bg-[#58a6ff]/20">
      <TopBar
        generatedAt={state?.generated_at}
        isRefreshing={isRefreshing}
        onRefresh={() => fetchState(true)}
      />

      <main className="flex-1 p-3 sm:p-4 md:p-6 max-w-5xl mx-auto w-full">
        {error && (
          <div className="mb-4 p-3.5 rounded-lg bg-[#3d1114]/40 border border-[#da3633]/60 text-[#f85149] text-xs flex items-center gap-2">
            <AlertCircle className="w-4 h-4 text-[#f85149] shrink-0" />
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
          /* ダッシュボード (縦並びレーン & ActiveAgent) */
          <div className="space-y-4">
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
              <LaneList
                statuses={state.meta.statuses}
                items={state.items}
                onSelectItem={handleSelectItem}
                onSelectRun={handleSelectRun}
              />
            ) : (
              <div className="py-20 text-center text-[#8b949e] font-mono text-xs">
                データを読み込み中…
              </div>
            )}
          </div>
        )}

        {/* Item詳細モーダル */}
        {targetItemKey && itemDetail && (
          <ItemDetailModal
            item={itemDetail.item}
            runs={itemDetail.runs}
            isLoadingRuns={isLoadingRuns}
            onClose={handleBackToDashboard}
            onSelectRun={handleSelectRun}
          />
        )}
      </main>
    </div>
  );
};
