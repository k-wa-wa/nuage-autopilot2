import React, { useEffect } from 'react';
import type { ItemView, RunView } from '../../types/api';
import { StatusBadge, RunBadge } from '../StatusBadge/StatusBadge';
import { X, ExternalLink, GitPullRequest, GitBranch, History, Terminal, Clock } from 'lucide-react';
import { fmtTime, fmtDuration } from '../../utils/format';

interface ItemDetailModalProps {
  item: ItemView;
  runs: RunView[];
  isLoadingRuns?: boolean;
  onClose: () => void;
  onSelectRun: (runId: number) => void;
}

export const ItemDetailModal: React.FC<ItemDetailModalProps> = ({
  item,
  runs,
  isLoadingRuns,
  onClose,
  onSelectRun,
}) => {
  // ESCキーで閉じる
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm animate-fade-in">
      <div
        className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-2xl max-h-[90vh] flex flex-col shadow-2xl shadow-cyan-950/20 overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* モーダルヘッダー */}
        <div className="p-5 border-b border-slate-800 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <span className="text-xl font-bold font-mono text-cyan-400">
              #{item.issue}
            </span>
            <div className="flex items-center gap-2">
              <span className="text-sm font-mono text-slate-300 font-semibold">{item.repo}</span>
              <StatusBadge status={item.status} />
            </div>
          </div>

          <button
            onClick={onClose}
            className="p-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-400 hover:text-white transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* モーダルボディ */}
        <div className="p-5 space-y-6 overflow-y-auto custom-scrollbar flex-1">
          {/* メタ情報 */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 bg-slate-950/60 p-4 rounded-xl border border-slate-800/80 text-xs">
            <div className="flex items-center justify-between">
              <span className="text-slate-500">GitHub Issue:</span>
              <a
                href={item.issue_url}
                target="_blank"
                rel="noreferrer"
                className="text-cyan-400 hover:underline flex items-center gap-1 font-mono"
              >
                開く <ExternalLink className="w-3 h-3" />
              </a>
            </div>

            {item.pr_number > 0 && (
              <div className="flex items-center justify-between">
                <span className="text-slate-500">プルリクエスト:</span>
                <a
                  href={item.pr_url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-purple-400 hover:underline flex items-center gap-1 font-mono"
                >
                  <GitPullRequest className="w-3 h-3" /> PR #{item.pr_number}
                </a>
              </div>
            )}

            {item.branch && (
              <div className="flex items-center justify-between sm:col-span-2">
                <span className="text-slate-500">ブランチ:</span>
                <span className="text-slate-300 font-mono flex items-center gap-1">
                  <GitBranch className="w-3 h-3 text-slate-500" />
                  {item.branch}
                </span>
              </div>
            )}

            <div className="flex items-center justify-between">
              <span className="text-slate-500">リトライ回数:</span>
              <span className="font-mono text-slate-300">{item.retry_count} 回</span>
            </div>

            <div className="flex items-center justify-between">
              <span className="text-slate-500">最終更新:</span>
              <span className="font-mono text-slate-300">{fmtTime(item.updated_at)}</span>
            </div>
          </div>

          {/* 実行履歴 (Runs) */}
          <div>
            <div className="flex items-center gap-2 mb-3">
              <History className="w-4 h-4 text-cyan-400" />
              <h3 className="text-sm font-semibold text-slate-200">実行履歴 ({runs.length} 件)</h3>
            </div>

            {isLoadingRuns ? (
              <div className="py-8 text-center text-xs text-slate-500 font-mono">
                履歴を読み込み中…
              </div>
            ) : runs.length === 0 ? (
              <div className="py-8 text-center text-xs text-slate-500 font-mono bg-slate-950/40 rounded-xl border border-slate-800">
                実行履歴はありません
              </div>
            ) : (
              <div className="space-y-2">
                {runs.map((run) => {
                  const duration =
                    run.started_at && run.ended_at
                      ? new Date(run.ended_at).getTime() - new Date(run.started_at).getTime()
                      : -1;

                  return (
                    <div
                      key={run.id}
                      className="flex items-center justify-between p-3 rounded-xl bg-slate-950/40 border border-slate-800/80 hover:border-slate-700 transition-colors text-xs"
                    >
                      <div className="flex items-center gap-3">
                        <span className="font-mono font-bold text-slate-400">#{run.id}</span>
                        <span className="uppercase font-mono bg-slate-800 px-2 py-0.5 rounded text-[11px] text-slate-300">
                          {run.phase}
                        </span>
                        <RunBadge run={run} />
                      </div>

                      <div className="flex items-center gap-4">
                        <div className="hidden sm:flex items-center gap-1 text-slate-500 font-mono text-[11px]">
                          <Clock className="w-3 h-3" />
                          <span>{fmtTime(run.started_at)}</span>
                          {duration > 0 && <span className="text-slate-400">({fmtDuration(duration)})</span>}
                        </div>

                        {run.has_log && (
                          <button
                            onClick={() => onSelectRun(run.id)}
                            className="inline-flex items-center gap-1 px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-cyan-300 hover:text-cyan-200 text-xs font-mono transition-colors"
                          >
                            <Terminal className="w-3.5 h-3.5" />
                            <span>ログ</span>
                          </button>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
};
