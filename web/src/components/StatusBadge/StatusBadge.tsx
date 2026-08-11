import React from 'react';
import type { RunView } from '../../types/api';
import { Loader2 } from 'lucide-react';

interface StatusBadgeProps {
  status: string;
  className?: string;
}

export const StatusBadge: React.FC<StatusBadgeProps> = ({ status, className = '' }) => {
  return (
    <span className={`text-xs font-semibold text-[#f0f6fc] tracking-tight ${className}`}>
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
    return <span className={`text-[#6e7681] text-xs font-mono ${className}`}>-</span>;
  }

  if (run.running) {
    return (
      <span className={`inline-flex items-center gap-1 text-xs text-[#d29922] font-mono font-medium ${className}`}>
        <Loader2 className="w-3 h-3 animate-spin" />
        <span>RUNNING</span>
      </span>
    );
  }

  if (!run.ended_at) {
    return <span className={`text-xs text-[#8b949e] font-mono ${className}`}>ABORTED</span>;
  }

  if (run.result === 'ok') {
    return <span className={`text-xs text-[#3fb950] font-mono font-medium ${className}`}>OK</span>;
  }

  return <span className={`text-xs text-[#f85149] font-mono font-medium ${className}`}>FAIL</span>;
};
