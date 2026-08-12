import React, { useState, useEffect, useRef } from 'react';
import type { RunView, LogView } from '../../types/api';
import { RunBadge } from '../StatusBadge/StatusBadge';
import {
  Terminal,
  ArrowLeft,
  ChevronDown,
  ChevronRight,
  Copy,
  Check,
  Radio,
  FileText,
  Clock,
  Sparkles,
} from 'lucide-react';
import { fmtTime, fmtDuration, fmtBytes } from '../../utils/format';

interface LogViewerProps {
  run: RunView;
  log?: LogView;
  logError?: string;
  isStreaming?: boolean;
  onBack: () => void;
}

export const LogViewer: React.FC<LogViewerProps> = ({
  run,
  log,
  logError,
  isStreaming = false,
  onBack,
}) => {
  const [showPrompt, setShowPrompt] = useState(false);
  const [autoScroll, setAutoScroll] = useState(true);
  const [copiedPrompt, setCopiedPrompt] = useState(false);
  const [copiedOutput, setCopiedOutput] = useState(false);
  const outputEndRef = useRef<HTMLDivElement>(null);

  // 自動スクロール
  useEffect(() => {
    if (autoScroll && outputEndRef.current) {
      outputEndRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [log?.output, autoScroll]);

  const copyToClipboard = async (text: string, type: 'prompt' | 'output') => {
    try {
      await navigator.clipboard.writeText(text);
      if (type === 'prompt') {
        setCopiedPrompt(true);
        setTimeout(() => setCopiedPrompt(false), 2000);
      } else {
        setCopiedOutput(true);
        setTimeout(() => setCopiedOutput(false), 2000);
      }
    } catch {
      // ignore
    }
  };

  const duration =
    run.started_at && run.ended_at
      ? new Date(run.ended_at).getTime() - new Date(run.started_at).getTime()
      : run.started_at && run.running
        ? Date.now() - new Date(run.started_at).getTime()
        : -1;

  return (
    <div className="space-y-4 max-w-5xl mx-auto pb-12 animate-fade-in">
      {/* ナビゲーション & ヘッダー */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-[#161b22] border border-[#30363d] p-3.5 sm:p-4 rounded-lg">
        <div className="flex items-center gap-3">
          <button
            onClick={onBack}
            className="p-1.5 rounded-md bg-[#21262d] hover:bg-[#30363d] text-[#c9d1d9] hover:text-white transition-colors border border-[#30363d]"
            title="戻る"
          >
            <ArrowLeft className="w-4 h-4" />
          </button>

          <div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono font-bold text-sm sm:text-base text-[#58a6ff]">Run #{run.id}</span>
              <span className="uppercase font-mono bg-[#21262d] border border-[#30363d] px-1.5 py-0.2 rounded text-[11px] text-[#c9d1d9] font-semibold">
                {run.phase}
              </span>
              {/* サマリ生成は特定の Issue に紐づかないため repo を持たない。 */}
              <span className="font-mono text-xs sm:text-sm text-[#c9d1d9] truncate max-w-[200px]">
                {run.repo ? `${run.repo} #${run.issue}` : 'パイプライン全体'}
              </span>
              <RunBadge run={run} />
            </div>

            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 mt-1 text-[11px] text-[#8b949e] font-mono">
              <div className="flex items-center gap-1">
                <Clock className="w-3 h-3" />
                <span>開始: {fmtTime(run.started_at)}</span>
                {duration > 0 && <span>({fmtDuration(duration)})</span>}
              </div>
              {log && <span>サイズ: {fmtBytes(log.size)}</span>}
            </div>
          </div>
        </div>

        {isStreaming && (
          <div className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-[#3b2300]/50 border border-[#9e6a03] text-[#d29922] text-xs font-mono self-start sm:self-auto animate-pulse">
            <Radio className="w-3.5 h-3.5 animate-spin" />
            <span>リアルタイム追従中</span>
          </div>
        )}
      </div>

      {logError && (
        <div className="p-3.5 rounded-lg bg-[#3d1114]/40 border border-[#da3633]/60 text-[#f85149] text-xs">
          {logError}
        </div>
      )}

      {/* プロンプトセクション */}
      {log?.prompt && (
        <div className="bg-[#161b22] border border-[#30363d] rounded-lg overflow-hidden">
          <button
            onClick={() => setShowPrompt(!showPrompt)}
            className="w-full p-3 flex items-center justify-between bg-[#161b22] hover:bg-[#21262d] text-left transition-colors"
          >
            <div className="flex items-center gap-2 text-xs font-medium text-[#c9d1d9] font-mono">
              {showPrompt ? <ChevronDown className="w-4 h-4 text-[#58a6ff]" /> : <ChevronRight className="w-4 h-4 text-[#8b949e]" />}
              <FileText className="w-4 h-4 text-[#58a6ff]" />
              <span>プロンプト ({log.prompt.length} 文字)</span>
              {log.prompt_truncated && (
                <span className="text-[#d29922] text-[11px]">(一部省略)</span>
              )}
            </div>

            <button
              onClick={(e) => {
                e.stopPropagation();
                copyToClipboard(log.prompt, 'prompt');
              }}
              className="p-1 px-2 rounded bg-[#21262d] hover:bg-[#30363d] text-[#8b949e] hover:text-white text-xs flex items-center gap-1 font-mono transition-colors border border-[#30363d]"
            >
              {copiedPrompt ? <Check className="w-3 h-3 text-[#3fb950]" /> : <Copy className="w-3 h-3" />}
              <span>{copiedPrompt ? 'コピー完了' : 'コピー'}</span>
            </button>
          </button>

          {showPrompt && (
            <div className="p-4 border-t border-[#30363d] bg-[#0d1117]">
              <pre className="text-xs font-mono text-[#c9d1d9] whitespace-pre-wrap break-words leading-relaxed selection:bg-[#58a6ff]/20">
                {log.prompt}
              </pre>
            </div>
          )}
        </div>
      )}

      {/* エージェント出力ログ */}
      <div className="bg-[#0d1117] border border-[#30363d] rounded-lg overflow-hidden shadow-sm">
        <div className="p-2.5 px-3.5 bg-[#161b22] border-b border-[#30363d] flex items-center justify-between">
          <div className="flex items-center gap-2 text-xs font-mono text-[#c9d1d9] font-semibold">
            <Terminal className="w-3.5 h-3.5 text-[#d29922]" />
            <span>エージェント出力ログ</span>
          </div>

          <div className="flex items-center gap-3 text-xs">
            <label className="flex items-center gap-1.5 text-[#8b949e] hover:text-[#c9d1d9] cursor-pointer font-mono text-[11px]">
              <input
                type="checkbox"
                checked={autoScroll}
                onChange={(e) => setAutoScroll(e.target.checked)}
                className="rounded bg-[#21262d] border-[#30363d] text-[#58a6ff] focus:ring-0"
              />
              自動スクロール
            </label>

            {log?.output && (
              <button
                onClick={() => copyToClipboard(log.output, 'output')}
                className="p-1 px-2 rounded bg-[#21262d] hover:bg-[#30363d] text-[#8b949e] hover:text-white text-xs flex items-center gap-1 font-mono transition-colors border border-[#30363d]"
              >
                {copiedOutput ? <Check className="w-3 h-3 text-[#3fb950]" /> : <Copy className="w-3 h-3" />}
                <span>{copiedOutput ? '完了' : 'コピー'}</span>
              </button>
            )}
          </div>
        </div>

        <div className="p-3.5 sm:p-4 max-h-[600px] overflow-y-auto font-mono text-xs text-[#c9d1d9] leading-relaxed custom-scrollbar bg-[#0d1117] selection:bg-[#d29922]/30 selection:text-[#f0f6fc]">
          {!log?.output ? (
            <div className="py-12 text-center text-[#8b949e] font-mono flex flex-col items-center gap-2">
              <Sparkles className="w-5 h-5 opacity-40 animate-pulse" />
              <span>ログ出力待機中…</span>
            </div>
          ) : (
            <pre className="whitespace-pre-wrap break-words">{log.output}</pre>
          )}
          <div ref={outputEndRef} />
        </div>
      </div>
    </div>
  );
};
