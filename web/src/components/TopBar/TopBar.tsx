import React from 'react';
import { Bot, RefreshCw } from 'lucide-react';
import { fmtTime } from '../../utils/format';

interface TopBarProps {
  generatedAt?: string | null;
  isRefreshing?: boolean;
  onRefresh?: () => void;
}

export const TopBar: React.FC<TopBarProps> = ({
  generatedAt,
  isRefreshing,
  onRefresh,
}) => {
  return (
    <header className="sticky top-0 z-30 bg-[#161b22] border-b border-[#30363d] px-4 py-2.5 flex items-center justify-between">
      {/* ブランドロゴ */}
      <a
        href="#/"
        className="flex items-center gap-2 font-semibold text-[#f0f6fc] hover:text-[#58a6ff] transition-colors"
      >
        <Bot className="w-4 h-4 text-[#8b949e]" />
        <span className="text-sm tracking-tight font-mono">autopilot</span>
      </a>

      {/* 右側: 参照専用 & 更新情報 */}
      <div className="flex items-center gap-3 sm:gap-4 text-xs text-[#8b949e]">
        <span className="text-[11px] font-mono">参照専用</span>

        {generatedAt && (
          <span className="font-mono text-[11px]">
            {fmtTime(generatedAt)}
          </span>
        )}

        {onRefresh && (
          <button
            onClick={onRefresh}
            disabled={isRefreshing}
            className="p-1 rounded text-[#8b949e] hover:text-[#c9d1d9] hover:bg-[#21262d] transition-colors disabled:opacity-50"
            title="手動更新"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${isRefreshing ? 'animate-spin text-[#58a6ff]' : ''}`} />
          </button>
        )}
      </div>
    </header>
  );
};
