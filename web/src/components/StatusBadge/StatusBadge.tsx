import React from 'react';
import type { RunView } from '../../types/api';
import { CheckCircle2, XCircle, Loader2, AlertTriangle, Minus } from 'lucide-react';

interface StatusBadgeProps {
  status: string;
  className?: string;
}

export const StatusBadge: React.FC<StatusBadgeProps> = ({ status, className = '' }) => {
  const getStyle = (s: string) => {
    switch (s.toLowerCase()) {
      case 'inbox':
        return 'bg-slate-800 text-slate-300 border-slate-700';
      case 'todo':
        return 'bg-sky-950/80 text-sky-300 border-sky-800/80';
      case 'in progress':
        return 'bg-amber-950/80 text-amber-300 border-amber-700 animate-pulse';
      case 'in review':
        return 'bg-purple-950/80 text-purple-300 border-purple-800/80';
      case 'verifying':
        return 'bg-cyan-950/80 text-cyan-300 border-cyan-800/80';
      case 'done':
        return 'bg-emerald-950/80 text-emerald-300 border-emerald-800/80';
      case 'blocked':
        return 'bg-rose-950/80 text-rose-300 border-rose-800/80';
      default:
        return 'bg-slate-800 text-slate-300 border-slate-700';
    }
  };

  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border tracking-wide ${getStyle(
        status,
      )} ${className}`}
    >
      {status}
    </span>
  );
};

interface RunBadgeProps {
  run?: RunView | null;
  className?: string;
}

export const RunBadge: React.FC<RunBadgeProps> = ({ run, className = '' }) => {
  if (!run) {
    return (
      <span className={`inline-flex items-center gap-1 text-xs text-slate-500 ${className}`}>
        <Minus className="w-3 h-3" /> 未実行
      </span>
    );
  }

  if (run.running) {
    return (
      <span
        className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-amber-500/10 text-amber-400 border border-amber-500/30 animate-pulse ${className}`}
      >
        <Loader2 className="w-3 h-3 animate-spin" />
        実行中
      </span>
    );
  }

  if (!run.ended_at) {
    return (
      <span
        className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-rose-500/10 text-rose-400 border border-rose-500/30 ${className}`}
      >
        <AlertTriangle className="w-3 h-3" />
        中断
      </span>
    );
  }

  if (run.result === 'ok') {
    return (
      <span
        className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/30 ${className}`}
      >
        <CheckCircle2 className="w-3 h-3" />
        成功
      </span>
    );
  }

  return (
    <span
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-rose-500/10 text-rose-400 border border-rose-500/30 ${className}`}
    >
      <XCircle className="w-3 h-3" />
      失敗
    </span>
  );
};
