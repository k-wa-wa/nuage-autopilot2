import React from 'react';
import type { Active } from '../../types/api';
import { Bot, Terminal, Clock, Layers, ArrowRight, Loader2 } from 'lucide-react';
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
      <div className="bg-slate-900/60 border border-slate-800 rounded-xl p-4 flex items-center justify-between shadow-sm">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 rounded-lg bg-slate-800 border border-slate-700 flex items-center justify-center text-slate-500">
            <Bot className="w-5 h-5" />
          </div>
          <div>
            <div className="text-sm font-medium text-slate-300">エージェント待機中</div>
            <div className="text-xs text-slate-500">
              {queueDepth > 0
                ? `待機キューに ${queueDepth} 件のジョブがあります`
                : '現在実行中のジョブはありません'}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-2 text-xs text-slate-400 font-mono bg-slate-950 px-3 py-1.5 rounded-lg border border-slate-800/80">
          <Layers className="w-3.5 h-3.5 text-slate-500" />
          <span>Queue: {queueDepth}</span>
        </div>
      </div>
    );
  }

  return (
    <div className="bg-gradient-to-r from-slate-900 via-amber-950/20 to-slate-900 border border-amber-500/30 rounded-xl p-4 shadow-lg shadow-amber-500/5 relative overflow-hidden">
      <div className="absolute top-0 right-0 w-32 h-32 bg-amber-500/10 rounded-full blur-2xl pointer-events-none" />

      <div className="flex flex-wrap items-center justify-between gap-4 relative z-10">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-amber-500/20 border border-amber-500/40 flex items-center justify-center text-amber-400 shadow-inner animate-pulse">
            <Loader2 className="w-5 h-5 animate-spin" />
          </div>

          <div>
            <div className="flex items-center gap-2">
              <span className="bg-amber-500/20 text-amber-300 border border-amber-500/30 px-2 py-0.5 rounded text-xs font-semibold uppercase tracking-wider">
                {active.phase}
              </span>
              <button
                onClick={() => onSelectIssue?.(active.repo, active.issue)}
                className="text-sm font-semibold text-slate-100 hover:text-cyan-300 transition-colors flex items-center gap-1 font-mono"
              >
                {active.repo} #{active.issue}
              </button>
            </div>

            <div className="flex flex-wrap items-center gap-4 mt-1.5 text-xs text-slate-400 font-mono">
              <div className="flex items-center gap-1">
                <Clock className="w-3.5 h-3.5 text-amber-400/80" />
                <span>ジョブ開始: {fmtTime(active.started_at)} ({fmtSince(active.started_at)}前)</span>
              </div>
              <div>
                <span>プロセス: </span>
                <span className="text-slate-300">
                  {active.agent_started_at
                    ? `${fmtSince(active.agent_started_at)}前 起動`
                    : '準備中 (同期・プロンプト生成)'}
                </span>
              </div>
            </div>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2 text-xs text-slate-400 font-mono bg-slate-950/80 px-3 py-1.5 rounded-lg border border-slate-800">
            <Layers className="w-3.5 h-3.5 text-amber-400" />
            <span>Queue: {queueDepth}</span>
          </div>

          {activeHasLog && (
            <button
              onClick={() => onSelectRun?.(active.run_id)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-amber-500 hover:bg-amber-400 text-slate-950 text-xs font-semibold shadow-md shadow-amber-500/20 transition-colors"
            >
              <Terminal className="w-3.5 h-3.5" />
              <span>ログをリアルタイム追従</span>
              <ArrowRight className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
      </div>
    </div>
  );
};
