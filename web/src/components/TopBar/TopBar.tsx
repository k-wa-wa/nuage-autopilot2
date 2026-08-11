import React from 'react';
import type { Meta } from '../../types/api';
import { Bot, GitFork, Lock, RefreshCw } from 'lucide-react';
import { fmtTime } from '../../utils/format';

interface TopBarProps {
  meta?: Meta | null;
  generatedAt?: string | null;
  isRefreshing?: boolean;
  onRefresh?: () => void;
}

export const TopBar: React.FC<TopBarProps> = ({
  meta,
  generatedAt,
  isRefreshing,
  onRefresh,
}) => {
  return (
    <header className="sticky top-0 z-30 bg-slate-900/90 backdrop-blur border-b border-slate-800 px-4 py-2.5 flex items-center justify-between gap-4">
      <div className="flex items-center gap-3">
        <a
          href="#/"
          className="flex items-center gap-2 font-bold text-lg text-slate-100 hover:text-cyan-400 transition-colors"
        >
          <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-cyan-500 to-blue-600 flex items-center justify-center shadow-lg shadow-cyan-500/20">
            <Bot className="w-4 h-4 text-slate-950" />
          </div>
          <span>autopilot</span>
        </a>

        {meta && (
          <div className="hidden md:flex items-center gap-2 pl-3 border-l border-slate-700 text-xs text-slate-400">
            <span className="font-mono bg-slate-800 px-2 py-0.5 rounded text-slate-300">
              {meta.project_owner} / #{meta.project_number}
            </span>
            <div className="flex items-center gap-1">
              <GitFork className="w-3.5 h-3.5 text-slate-500" />
              <span>{meta.repos.length} repos</span>
            </div>
            {meta.agents && meta.agents.length > 0 && (
              <div className="hidden lg:flex items-center gap-1.5 ml-2">
                {meta.agents.map((ag) => (
                  <span
                    key={ag.use}
                    className="bg-slate-800/80 border border-slate-700/60 px-1.5 py-0.5 rounded text-[11px] font-mono text-cyan-300/90"
                    title={`Command: ${ag.command} | Timeout: ${ag.timeout}`}
                  >
                    {ag.use}: {ag.model || ag.command}
                  </span>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      <div className="flex items-center gap-3 text-xs">
        <div className="hidden sm:flex items-center gap-1.5 px-2 py-1 rounded bg-slate-800/80 border border-slate-700/60 text-slate-400">
          <Lock className="w-3 h-3 text-amber-400" />
          <span>参照専用 (Read-Only)</span>
        </div>

        {generatedAt && (
          <div className="text-slate-400 font-mono hidden sm:block">
            {fmtTime(generatedAt)}
          </div>
        )}

        {onRefresh && (
          <button
            onClick={onRefresh}
            disabled={isRefreshing}
            className="p-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-white transition-colors border border-slate-700 disabled:opacity-50"
            title="手動更新"
          >
            <RefreshCw className={`w-4 h-4 ${isRefreshing ? 'animate-spin text-cyan-400' : ''}`} />
          </button>
        )}
      </div>
    </header>
  );
};
