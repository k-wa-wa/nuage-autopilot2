import React from 'react';
import type { SummaryResponse, SummaryTodo, Urgency } from '../../types/api';
import { ClipboardList, ChevronDown, ChevronUp, CalendarClock, ExternalLink } from 'lucide-react';
import { fmtTime, fmtSince } from '../../utils/format';

interface SummaryPanelProps {
  summary?: SummaryResponse | null;
  // onSelectHistory は履歴の 1 件を表示するときに呼ぶ。undefined なら最新に戻す。
  onSelectHistory?: (id?: number) => void;
  // selectedId は表示中のサマリ。最新を見ている場合は undefined。
  selectedId?: number;
}

// 緊急度ごとの見た目。判断の速さがこのパネルの価値なので、色で先に伝える。
const URGENCY: Record<Urgency, { label: string; className: string }> = {
  high: { label: '要対応', className: 'bg-[#3d1114]/60 text-[#f85149] border-[#da3633]/60' },
  medium: { label: '通常', className: 'bg-[#3b2300]/40 text-[#d29922] border-[#9e6a03]/60' },
  low: { label: '低', className: 'bg-[#21262d] text-[#8b949e] border-[#30363d]' },
};

const URGENCY_ORDER: Urgency[] = ['high', 'medium', 'low'];

function issueUrl(todo: SummaryTodo): string | null {
  if (!todo.repo || !todo.issue) return null;
  return `https://github.com/${todo.repo}/issues/${todo.issue}`;
}

export const SummaryPanel: React.FC<SummaryPanelProps> = ({
  summary,
  onSelectHistory,
  selectedId,
}) => {
  const [showHistory, setShowHistory] = React.useState(false);

  const current = summary?.current ?? null;
  const report = current?.report ?? null;
  const todos = React.useMemo(() => {
    const list = report?.todos ?? [];
    return [...list].sort(
      (a, b) => URGENCY_ORDER.indexOf(a.urgency) - URGENCY_ORDER.indexOf(b.urgency),
    );
  }, [report]);

  // 定期生成が無効で、過去の生成物も無ければ何も出さない（画面を余計に埋めない）。
  if (!summary || (!summary.schedule && !summary.current)) return null;

  return (
    <section className="bg-[#161b22] border border-[#30363d] rounded-md overflow-hidden">
      {/* ヘッダー */}
      <div className="px-3.5 py-2 border-b border-[#30363d] flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <ClipboardList className="w-4 h-4 text-[#8b949e] shrink-0" />
          <span className="text-xs font-semibold text-[#f0f6fc] font-mono">TODO</span>
          {current && (
            <span className="text-[11px] text-[#8b949e] font-mono truncate">
              {fmtTime(current.created_at)}（{fmtSince(current.created_at)}前）
            </span>
          )}
        </div>

        <div className="flex items-center gap-3 text-[11px] font-mono text-[#8b949e]">
          {summary.next_at && (
            <span className="flex items-center gap-1" title={`cron: ${summary.schedule}`}>
              <CalendarClock className="w-3 h-3" />
              次回 {fmtTime(summary.next_at)}
            </span>
          )}
          {summary.history.length > 1 && (
            <button
              onClick={() => setShowHistory((v) => !v)}
              className="flex items-center gap-1 text-[#58a6ff] hover:underline"
            >
              履歴
              {showHistory ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
            </button>
          )}
        </div>
      </div>

      {/* 履歴 */}
      {showHistory && (
        <ul className="divide-y divide-[#21262d] border-b border-[#30363d] bg-[#0d1117]/30">
          {summary.history.map((h) => (
            <li key={h.id}>
              <button
                onClick={() => onSelectHistory?.(h.id === summary.history[0].id ? undefined : h.id)}
                className={`w-full text-left px-3.5 py-2 text-[11px] font-mono hover:bg-[#21262d]/50 flex items-center justify-between gap-3 ${
                  (selectedId ?? summary.history[0]?.id) === h.id ? 'text-[#f0f6fc]' : 'text-[#8b949e]'
                }`}
              >
                <span className="truncate">{h.headline || '(内容を解釈できませんでした)'}</span>
                <span className="shrink-0">
                  {h.todo_count > 0 && <span className="mr-2">{h.todo_count} 件</span>}
                  {fmtTime(h.created_at)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {/* 本体 */}
      {!current ? (
        <div className="p-3.5 text-xs text-[#8b949e] font-mono">
          まだ生成されていない。次回の生成を待っている。
        </div>
      ) : !report ? (
        // JSON として読めなかった場合は生の出力を見せる（生成を無駄にしないため）。
        <div className="p-3.5 space-y-2">
          <p className="text-[11px] text-[#d29922] font-mono">
            出力を解釈できなかったため、そのまま表示している。
          </p>
          <pre className="text-[11px] text-[#c9d1d9] font-mono whitespace-pre-wrap break-words max-h-64 overflow-y-auto">
            {current.raw}
          </pre>
        </div>
      ) : (
        <div className="divide-y divide-[#21262d]">
          {report.headline && (
            <p className="px-3.5 py-2.5 text-xs text-[#f0f6fc]">{report.headline}</p>
          )}

          {todos.length === 0 ? (
            <p className="px-3.5 py-2.5 text-xs text-[#8b949e] font-mono">
              対応が必要なTODOはありません。
            </p>
          ) : (
            <ul className="divide-y divide-[#21262d]">
              {todos.map((todo, i) => {
                const urgency = URGENCY[todo.urgency] ?? URGENCY.medium;
                const url = issueUrl(todo);
                return (
                  <li key={`${todo.repo}#${todo.issue}-${i}`} className="px-3.5 py-2.5 space-y-1.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <span
                        className={`px-1.5 rounded text-[11px] font-mono border ${urgency.className}`}
                      >
                        {urgency.label}
                      </span>
                      <span className="text-xs font-semibold text-[#f0f6fc]">{todo.title}</span>
                      {url && (
                        <a
                          href={url}
                          target="_blank"
                          rel="noreferrer"
                          className="inline-flex items-center gap-1 text-[11px] text-[#58a6ff] hover:underline font-mono"
                          title="Issue を新規タブで開く"
                        >
                          {todo.repo} #{todo.issue}
                          <ExternalLink className="w-3 h-3" />
                        </a>
                      )}
                      {todo.status && (
                        <span className="text-[11px] text-[#8b949e] font-mono">{todo.status}</span>
                      )}
                    </div>
                    {todo.why && <p className="text-[11px] text-[#8b949e]">{todo.why}</p>}
                    {todo.action && (
                      <p className="text-[11px] text-[#c9d1d9]">
                        <span className="text-[#8b949e] font-mono">対応: </span>
                        {todo.action}
                      </p>
                    )}
                  </li>
                );
              })}
            </ul>
          )}

          {report.notes && (
            <p className="px-3.5 py-2.5 text-[11px] text-[#8b949e]">{report.notes}</p>
          )}
        </div>
      )}
    </section>
  );
};
