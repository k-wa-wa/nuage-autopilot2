import React from 'react';
import type { ItemView } from '../../types/api';
import { RunBadge } from '../StatusBadge/StatusBadge';
import { GitPullRequest, GitBranch, AlertCircle, ExternalLink, Terminal, Clock } from 'lucide-react';
import { fmtSince } from '../../utils/format';

interface ItemCardProps {
  item: ItemView;
  onSelect: (item: ItemView) => void;
  onSelectRun?: (runId: number) => void;
}

const STALE_MS = 60 * 60 * 1000; // 1時間以上経過でstale

export const ItemCard: React.FC<ItemCardProps> = ({ item, onSelect, onSelectRun }) => {
  const isStale =
    item.reconciled_at && Date.now() - new Date(item.reconciled_at).getTime() > STALE_MS;

  return (
    <div
      onClick={() => onSelect(item)}
      className={`group bg-slate-900 border rounded-xl p-3.5 shadow-sm hover:shadow-md transition-all cursor-pointer relative overflow-hidden ${
        item.running
          ? 'border-amber-500/60 shadow-amber-500/10 ring-1 ring-amber-500/40 bg-gradient-to-br from-slate-900 via-amber-950/10 to-slate-900'
          : isStale
            ? 'border-slate-800/60 opacity-60 hover:opacity-100'
            : 'border-slate-800 hover:border-slate-700'
      }`}
    >
      {/* 実行中インジケータバー */}
      {item.running && (
        <div className="absolute top-0 left-0 right-0 h-1 bg-gradient-to-r from-amber-500 to-cyan-500 animate-pulse" />
      )}

      <div className="flex items-start justify-between gap-2 mb-2">
        <div className="flex items-center gap-1.5 min-w-0">
          <span className="text-xs font-mono font-bold text-cyan-400 group-hover:text-cyan-300">
            #{item.issue}
          </span>
          <span className="text-xs text-slate-400 truncate font-mono" title={item.repo}>
            {item.repo.split('/')[1] || item.repo}
          </span>
        </div>

        <RunBadge run={item.last_run} />
      </div>

      <div className="space-y-1.5 mb-3 text-xs text-slate-400">
        {item.pr_number > 0 && (
          <div className="flex items-center gap-1.5 text-purple-300/90 font-mono">
            <GitPullRequest className="w-3.5 h-3.5 text-purple-400 shrink-0" />
            <a
              href={item.pr_url}
              target="_blank"
              rel="noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="hover:underline flex items-center gap-1"
            >
              PR #{item.pr_number}
              <ExternalLink className="w-2.5 h-2.5 opacity-60" />
            </a>
          </div>
        )}

        {item.branch && (
          <div className="flex items-center gap-1.5 text-slate-400 font-mono text-[11px] truncate">
            <GitBranch className="w-3 h-3 text-slate-500 shrink-0" />
            <span className="truncate">{item.branch}</span>
          </div>
        )}

        {item.retry_count > 0 && (
          <div className="flex items-center gap-1 text-amber-400 text-[11px]">
            <AlertCircle className="w-3 h-3" />
            <span>リトライ: {item.retry_count} 回</span>
          </div>
        )}
      </div>

      <div className="pt-2 border-t border-slate-800/80 flex items-center justify-between text-[11px] text-slate-500 font-mono">
        <div className="flex items-center gap-1">
          <Clock className="w-3 h-3" />
          <span>{fmtSince(item.updated_at || item.reconciled_at)}前</span>
        </div>

        {item.last_run?.has_log && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              if (item.last_run) onSelectRun?.(item.last_run.id);
            }}
            className="p-1 rounded hover:bg-slate-800 text-slate-400 hover:text-cyan-300 transition-colors"
            title="直近のログを見る"
          >
            <Terminal className="w-3.5 h-3.5" />
          </button>
        )}
      </div>
    </div>
  );
};
