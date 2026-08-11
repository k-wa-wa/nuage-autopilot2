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
    <div className="space-y-4 max-w-6xl mx-auto pb-12 animate-fade-in">
      {/* ナビゲーション & ヘッダー */}
      <div className="flex flex-wrap items-center justify-between gap-4 bg-slate-900 border border-slate-800 p-4 rounded-2xl">
        <div className="flex items-center gap-3">
          <button
            onClick={onBack}
            className="p-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-white transition-colors border border-slate-700/60"
            title="戻る"
          >
            <ArrowLeft className="w-4 h-4" />
          </button>

          <div>
            <div className="flex items-center gap-2.5">
              <span className="font-mono font-bold text-lg text-cyan-400">Run #{run.id}</span>
              <span className="uppercase font-mono bg-slate-800 border border-slate-700 px-2 py-0.5 rounded text-xs text-slate-300 font-semibold">
                {run.phase}
              </span>
              <span className="font-mono text-sm text-slate-300">
                {run.repo} #{run.issue}
              </span>
              <RunBadge run={run} />
            </div>

            <div className="flex items-center gap-4 mt-1 text-xs text-slate-500 font-mono">
              <div className="flex items-center gap-1">
                <Clock className="w-3.5 h-3.5" />
                <span>開始: {fmtTime(run.started_at)}</span>
                {duration > 0 && <span>({fmtDuration(duration)})</span>}
              </div>
              {log && <span>ログサイズ: {fmtBytes(log.size)}</span>}
            </div>
          </div>
        </div>

        {isStreaming && (
          <div className="flex items-center gap-2 px-3 py-1.5 rounded-xl bg-amber-500/10 border border-amber-500/30 text-amber-400 text-xs font-mono animate-pulse">
            <Radio className="w-3.5 h-3.5 animate-spin" />
            <span>リアルタイム追従中</span>
          </div>
        )}
      </div>

      {logError && (
        <div className="p-4 rounded-xl bg-rose-950/40 border border-rose-800/80 text-rose-300 text-sm">
          {logError}
        </div>
      )}

      {/* プロンプトセクション */}
      {log?.prompt && (
        <div className="bg-slate-900 border border-slate-800 rounded-2xl overflow-hidden shadow-sm">
          <button
            onClick={() => setShowPrompt(!showPrompt)}
            className="w-full p-3.5 flex items-center justify-between bg-slate-900/90 hover:bg-slate-850 text-left transition-colors"
          >
            <div className="flex items-center gap-2 text-xs font-semibold text-slate-300 font-mono">
              {showPrompt ? <ChevronDown className="w-4 h-4 text-cyan-400" /> : <ChevronRight className="w-4 h-4 text-slate-500" />}
              <FileText className="w-4 h-4 text-cyan-400" />
              <span>プロンプト ({log.prompt.length} 文字)</span>
              {log.prompt_truncated && (
                <span className="text-amber-400 text-[11px]">(一部省略表示)</span>
              )}
            </div>

            <button
              onClick={(e) => {
                e.stopPropagation();
                copyToClipboard(log.prompt, 'prompt');
              }}
              className="p-1 px-2 rounded bg-slate-800 hover:bg-slate-700 text-slate-400 hover:text-white text-xs flex items-center gap-1 font-mono transition-colors"
            >
              {copiedPrompt ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
              <span>{copiedPrompt ? 'コピー完了' : 'コピー'}</span>
            </button>
          </button>

          {showPrompt && (
            <div className="p-4 border-t border-slate-800/80 bg-slate-950/80">
              <pre className="text-xs font-mono text-slate-300 whitespace-pre-wrap break-words leading-relaxed selection:bg-cyan-500/20">
                {log.prompt}
              </pre>
            </div>
          )}
        </div>
      )}

      {/* エージェント出力ログ */}
      <div className="bg-slate-950 border border-slate-800 rounded-2xl overflow-hidden shadow-xl">
        <div className="p-3 bg-slate-900 border-b border-slate-800 flex items-center justify-between">
          <div className="flex items-center gap-2 text-xs font-mono text-slate-300 font-semibold">
            <Terminal className="w-4 h-4 text-amber-400" />
            <span>エージェント出力ログ</span>
          </div>

          <div className="flex items-center gap-3 text-xs">
            <label className="flex items-center gap-1.5 text-slate-400 hover:text-slate-200 cursor-pointer font-mono text-[11px]">
              <input
                type="checkbox"
                checked={autoScroll}
                onChange={(e) => setAutoScroll(e.target.checked)}
                className="rounded bg-slate-800 border-slate-700 text-cyan-500 focus:ring-0"
              />
              自動スクロール
            </label>

            {log?.output && (
              <button
                onClick={() => copyToClipboard(log.output, 'output')}
                className="p-1 px-2 rounded bg-slate-800 hover:bg-slate-700 text-slate-400 hover:text-white text-xs flex items-center gap-1 font-mono transition-colors"
              >
                {copiedOutput ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
                <span>{copiedOutput ? 'コピー完了' : 'コピー'}</span>
              </button>
            )}
          </div>
        </div>

        <div className="p-4 max-h-[650px] overflow-y-auto font-mono text-xs text-slate-200 leading-relaxed custom-scrollbar bg-slate-950/90 selection:bg-amber-500/30 selection:text-amber-200">
          {!log?.output ? (
            <div className="py-12 text-center text-slate-600 font-mono flex flex-col items-center gap-2">
              <Sparkles className="w-6 h-6 opacity-40 animate-pulse" />
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
