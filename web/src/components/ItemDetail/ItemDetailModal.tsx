import React, { useEffect } from 'react';
import type { ItemView, RunView } from '../../types/api';
import { StatusBadge, RunBadge } from '../StatusBadge/StatusBadge';
import { X, CircleDot, GitPullRequest, GitBranch, History, Terminal, Clock } from 'lucide-react';
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
    <div
      className="fixed inset-0 z-50 flex items-end sm:items-center justify-center p-0 sm:p-4 bg-black/70 backdrop-blur-xs animate-fade-in"
      onClick={onClose}
    >
      <div
        className="bg-[#161b22] border border-[#30363d] rounded-t-2xl sm:rounded-xl w-full max-w-2xl max-h-[85vh] sm:max-h-[90vh] flex flex-col shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* モーダルヘッダー */}
        <div className="p-4 sm:p-4.5 border-b border-[#30363d] flex items-center justify-between">
          <div className="flex items-center gap-2.5 min-w-0">
            <span className="text-lg sm:text-xl font-bold font-mono text-[#58a6ff]">
              #{item.issue}
            </span>
            <div className="flex items-center gap-2 min-w-0">
              <span className="text-xs sm:text-sm font-mono text-[#c9d1d9] truncate font-semibold">
                {item.repo}
              </span>
              <StatusBadge status={item.status} />
            </div>
          </div>

          <button
            onClick={onClose}
            className="p-1 rounded-md bg-[#21262d] hover:bg-[#30363d] text-[#8b949e] hover:text-white transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* モーダルボディ */}
        <div className="p-4 sm:p-5 space-y-5 overflow-y-auto custom-scrollbar flex-1">
          {/* メタ情報テーブル */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5 bg-[#0d1117] p-3.5 rounded-lg border border-[#30363d] text-xs">
            <div className="flex items-center justify-between">
              <span className="text-[#8b949e]">Issue:</span>
              <a
                href={item.issue_url}
                target="_blank"
                rel="noreferrer"
                className="text-[#58a6ff] hover:underline flex items-center gap-1 font-mono"
              >
                <CircleDot className="w-3 h-3" /> #{item.issue}
              </a>
            </div>

            {item.pr_number > 0 && (
              <div className="flex items-center justify-between">
                <span className="text-[#8b949e]">PR:</span>
                <a
                  href={item.pr_url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-[#58a6ff] hover:underline flex items-center gap-1 font-mono"
                >
                  <GitPullRequest className="w-3 h-3" /> #{item.pr_number}
                </a>
              </div>
            )}

            {item.branch && (
              <div className="flex items-center justify-between sm:col-span-2">
                <span className="text-[#8b949e]">ブランチ:</span>
                <span className="text-[#c9d1d9] font-mono flex items-center gap-1 truncate max-w-[280px]">
                  <GitBranch className="w-3 h-3 text-[#8b949e] shrink-0" />
                  <span className="truncate">{item.branch}</span>
                </span>
              </div>
            )}

            <div className="flex items-center justify-between">
              <span className="text-[#8b949e]">リトライ回数:</span>
              <span className="font-mono text-[#c9d1d9]">{item.retry_count} 回</span>
            </div>

            <div className="flex items-center justify-between">
              <span className="text-[#8b949e]">最終更新:</span>
              <span className="font-mono text-[#c9d1d9]">{fmtTime(item.updated_at || item.reconciled_at)}</span>
            </div>
          </div>

          {/* 実行履歴 (Runs) */}
          <div>
            <div className="flex items-center gap-2 mb-2.5">
              <History className="w-3.5 h-3.5 text-[#58a6ff]" />
              <h3 className="text-xs font-semibold text-[#c9d1d9] font-mono">
                実行履歴 ({runs.length} 件)
              </h3>
            </div>

            {isLoadingRuns ? (
              <div className="py-6 text-center text-xs text-[#8b949e] font-mono">
                履歴を読み込み中…
              </div>
            ) : runs.length === 0 ? (
              <div className="py-6 text-center text-xs text-[#8b949e] font-mono bg-[#0d1117] rounded-lg border border-[#30363d]">
                実行履歴はありません
              </div>
            ) : (
              <div className="space-y-1.5">
                {runs.map((run) => {
                  const duration =
                    run.started_at && run.ended_at
                      ? new Date(run.ended_at).getTime() - new Date(run.started_at).getTime()
                      : -1;

                  return (
                    <div
                      key={run.id}
                      className="flex items-center justify-between p-2.5 rounded-lg bg-[#0d1117] border border-[#30363d] text-xs"
                    >
                      <div className="flex items-center gap-2">
                        <span className="font-mono font-semibold text-[#8b949e]">#{run.id}</span>
                        <span className="uppercase font-mono bg-[#21262d] px-1.5 py-0.5 rounded text-[11px] text-[#c9d1d9]">
                          {run.phase}
                        </span>
                        <RunBadge run={run} />
                      </div>

                      <div className="flex items-center gap-3">
                        <div className="hidden sm:flex items-center gap-1 text-[#8b949e] font-mono text-[11px]">
                          <Clock className="w-3 h-3" />
                          <span>{fmtTime(run.started_at)}</span>
                          {duration > 0 && <span className="text-[#c9d1d9]">({fmtDuration(duration)})</span>}
                        </div>

                        {run.has_log && (
                          <button
                            onClick={() => onSelectRun(run.id)}
                            className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-[#21262d] hover:bg-[#30363d] text-[#58a6ff] hover:text-white text-xs font-mono transition-colors border border-[#30363d]"
                          >
                            <Terminal className="w-3 h-3" />
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
