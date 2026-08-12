import React from 'react';
import type { Active } from '../../types/api';
import { Bot, Clock, Layers, ArrowRight, Loader2 } from 'lucide-react';
import { fmtTime, fmtSince } from '../../utils/format';

interface ActiveAgentProps {
  active?: Active | null;
  queueDepth: number;
  activeHasLog?: boolean;
  onSelectRun?: (runId: number) => void;
  onSelectIssue?: (repo: string, issue: number) => void;
}

export const ActiveAgent: React.FC<ActiveAgentProps> = ({
  active,
  queueDepth,
  activeHasLog,
  onSelectRun,
  onSelectIssue,
}) => {
  if (!active) {
    return (
      <div className="bg-[#161b22] border border-[#30363d] rounded-md p-3 sm:p-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs">
        <div className="flex items-center gap-2.5">
          <Bot className="w-4 h-4 text-[#8b949e]" />
          <span className="text-[#8b949e]">
            {queueDepth > 0
              ? `待機中（キューに ${queueDepth} 件）`
              : '待機中。実行中のエージェントはない。'}
          </span>
        </div>

        {queueDepth > 0 && (
          <div className="flex items-center gap-1 text-[#8b949e] font-mono text-[11px]">
            <Layers className="w-3 h-3" />
            <span>Queue: {queueDepth}</span>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="bg-[#161b22] border border-[#30363d] rounded-md p-3 sm:p-4 text-xs">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-3">
        <div className="flex items-start sm:items-center gap-2.5 min-w-0">
          <div className="w-6 h-6 rounded bg-[#21262d] flex items-center justify-center text-[#d29922] shrink-0 mt-0.5 sm:mt-0">
            <Loader2 className="w-3.5 h-3.5 animate-spin" />
          </div>

          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="bg-[#21262d] text-[#c9d1d9] border border-[#30363d] px-1.5 py-0.2 rounded text-[11px] font-mono uppercase">
                {active.phase}
              </span>
              {/* サマリ生成は特定の Issue に紐づかないので、リンク先を持たない。 */}
              {active.repo ? (
                <button
                  onClick={() => onSelectIssue?.(active.repo, active.issue)}
                  className="font-semibold text-[#f0f6fc] hover:text-[#58a6ff] transition-colors font-mono truncate"
                >
                  {active.repo} #{active.issue}
                </button>
              ) : (
                <span className="font-semibold text-[#f0f6fc] font-mono truncate">
                  パイプライン全体
                </span>
              )}
              <span className="text-[11px] text-[#d29922] font-mono">
                実行中
              </span>
            </div>

            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 mt-1 text-[11px] text-[#8b949e] font-mono">
              <div className="flex items-center gap-1">
                <Clock className="w-3 h-3 text-[#8b949e]" />
                <span>開始: {fmtTime(active.started_at)} ({fmtSince(active.started_at)}前)</span>
              </div>
              <div className="truncate">
                <span>プロセス: </span>
                <span className="text-[#c9d1d9]">
                  {active.agent_started_at
                    ? `${fmtSince(active.agent_started_at)}前 起動`
                    : '準備中'}
                </span>
              </div>
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between sm:justify-end gap-3 pt-2 sm:pt-0 border-t sm:border-t-0 border-[#21262d]">
          <div className="flex items-center gap-1 text-[11px] text-[#8b949e] font-mono">
            <Layers className="w-3 h-3" />
            <span>Queue: {queueDepth}</span>
          </div>

          {activeHasLog && (
            <button
              onClick={() => onSelectRun?.(active.run_id)}
              className="inline-flex items-center gap-1 text-[11px] text-[#58a6ff] hover:underline font-mono"
            >
              <span>ログを見る</span>
              <ArrowRight className="w-3 h-3" />
            </button>
          )}
        </div>
      </div>
    </div>
  );
};
