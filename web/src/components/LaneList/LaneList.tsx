import React from 'react';
import type { ItemView } from '../../types/api';
import { RunBadge } from '../StatusBadge/StatusBadge';
import { fmtTime, fmtSince } from '../../utils/format';

interface LaneListProps {
  statuses: string[];
  items: ItemView[];
  onSelectItem: (item: ItemView) => void;
  onSelectRun?: (runId: number) => void;
}

const LANE_LIMIT = 50;
const STALE_MS = 60 * 60 * 1000; // 1 時間

export const LaneList: React.FC<LaneListProps> = ({
  statuses,
  items,
  onSelectItem,
}) => {
  // Project 上で確認できているアクティブなアイテムのみに絞り込む
  // (reconciled_at が null または 1 時間以上古いデータは除外)
  const activeItems = React.useMemo(() => {
    const now = Date.now();
    return items.filter((it) => {
      if (!it.reconciled_at) return false;
      const reconciledTime = new Date(it.reconciled_at).getTime();
      if (isNaN(reconciledTime)) return false;
      return now - reconciledTime <= STALE_MS;
    });
  }, [items]);

  const byStatus = React.useMemo(() => {
    const map = new Map<string, ItemView[]>();
    for (const it of activeItems) {
      const list = map.get(it.status) || [];
      list.push(it);
      map.set(it.status, list);
    }
    return map;
  }, [activeItems]);

  const orderedStatuses = React.useMemo(() => {
    const list = [...statuses];
    for (const s of byStatus.keys()) {
      if (!list.includes(s)) list.push(s);
    }
    return list;
  }, [statuses, byStatus]);

  return (
    <div className="space-y-3.5">
      {orderedStatuses.map((status) => {
        const laneItems = byStatus.get(status) || [];

        const sorted = [...laneItems].sort(
          (a, b) =>
            new Date(b.updated_at || b.reconciled_at || 0).getTime() -
            new Date(a.updated_at || a.reconciled_at || 0).getTime(),
        );

        const visibleItems = sorted.slice(0, LANE_LIMIT);
        const hiddenCount = sorted.length - visibleItems.length;

        return (
          <section
            key={status}
            className="bg-[#161b22] border border-[#30363d] rounded-md overflow-hidden"
          >
            {/* レーンヘッダー */}
            <div className="px-3.5 py-2 bg-[#161b22] border-b border-[#30363d] flex items-center justify-between">
              <div className="flex items-center gap-2 font-mono">
                <span className="text-xs font-semibold text-[#f0f6fc]">
                  {status || '(Status 未設定)'}
                </span>
                <span className="text-[11px] text-[#8b949e]">
                  {sorted.length} 件
                </span>
              </div>
            </div>

            {/* PC用テーブル表示 (md以上) - table-fixed で全セクションの列幅を完全に統一 */}
            <div className="hidden md:block overflow-x-auto">
              <table className="w-full text-left text-xs border-collapse font-sans table-fixed">
                <thead>
                  <tr className="border-b border-[#30363d] bg-[#0d1117]/30 text-[#8b949e] font-medium text-[11px]">
                    <th className="py-2 px-3 font-normal w-[30%]">Issue</th>
                    <th className="py-2 px-3 font-normal w-[12%]">PR</th>
                    <th className="py-2 px-3 font-normal w-[24%]">ブランチ</th>
                    <th className="py-2 px-3 font-normal text-center w-[8%]">リトライ</th>
                    <th className="py-2 px-3 font-normal w-[10%]">結果</th>
                    <th className="py-2 px-3 font-normal w-[16%]">更新</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#21262d]">
                  {visibleItems.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="py-3 px-3 text-center text-[11px] text-[#6e7681] font-mono">
                        アイテムなし
                      </td>
                    </tr>
                  ) : (
                    visibleItems.map((it) => {
                      return (
                        <tr
                          key={`${it.repo}#${it.issue}`}
                          onClick={() => onSelectItem(it)}
                          className={`hover:bg-[#21262d]/50 cursor-pointer transition-colors ${
                            it.running ? 'bg-[#3b2300]/10' : ''
                          }`}
                        >
                          {/* Issue 列 */}
                          <td className="py-2.5 px-3 truncate">
                            <div className="flex items-center gap-1.5 font-mono truncate">
                              <a
                                href={it.issue_url}
                                target="_blank"
                                rel="noreferrer"
                                onClick={(e) => e.stopPropagation()}
                                className="text-[#58a6ff] hover:underline font-semibold shrink-0"
                                title="Issue を新規タブで開く"
                              >
                                #{it.issue}
                              </a>
                              <span className="text-[#8b949e] truncate" title={it.repo}>
                                {it.repo}
                              </span>
                            </div>
                          </td>

                          {/* PR 列 */}
                          <td className="py-2.5 px-3 font-mono text-[11px] truncate">
                            {it.pr_number ? (
                              <a
                                href={it.pr_url}
                                target="_blank"
                                rel="noreferrer"
                                onClick={(e) => e.stopPropagation()}
                                className="text-[#58a6ff] hover:underline"
                                title="PR を新規タブで開く"
                              >
                                #{it.pr_number}
                              </a>
                            ) : (
                              <span className="text-[#6e7681]">-</span>
                            )}
                          </td>

                          {/* ブランチ 列 */}
                          <td className="py-2.5 px-3 font-mono text-[11px] text-[#8b949e] truncate">
                            {it.branch ? (
                              <span className="truncate inline-block max-w-full" title={it.branch}>
                                {it.branch}
                              </span>
                            ) : (
                              <span className="text-[#6e7681]">-</span>
                            )}
                          </td>

                          {/* リトライ 列 */}
                          <td className="py-2.5 px-3 text-center font-mono text-[11px]">
                            {it.retry_count > 0 ? (
                              <span className="text-[#d29922] font-medium">{it.retry_count}</span>
                            ) : (
                              <span className="text-[#6e7681]">0</span>
                            )}
                          </td>

                          {/* 結果 列 */}
                          <td className="py-2.5 px-3 font-mono truncate">
                            <RunBadge run={it.last_run} />
                          </td>

                          {/* 更新 列 (左寄せ) */}
                          <td className="py-2.5 px-3 font-mono text-[11px] text-[#8b949e] truncate">
                            {fmtTime(it.updated_at || it.reconciled_at)}
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>

            {/* モバイル用表示 (md未満) */}
            <div className="block md:hidden divide-y divide-[#21262d]">
              {visibleItems.length === 0 ? (
                <div className="p-3 text-center text-[11px] text-[#6e7681] font-mono">
                  アイテムなし
                </div>
              ) : (
                visibleItems.map((it) => {
                  return (
                    <div
                      key={`${it.repo}#${it.issue}`}
                      onClick={() => onSelectItem(it)}
                      className={`p-3 space-y-1.5 cursor-pointer active:bg-[#21262d] ${
                        it.running ? 'bg-[#3b2300]/10' : ''
                      }`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1.5 font-mono text-xs font-semibold">
                          <a
                            href={it.issue_url}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            className="text-[#58a6ff] hover:underline"
                            title="Issue を新規タブで開く"
                          >
                            #{it.issue}
                          </a>
                          <span className="text-[#8b949e] text-[11px] truncate max-w-[150px]">
                            {it.repo}
                          </span>
                        </div>

                        <RunBadge run={it.last_run} />
                      </div>

                      <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-[#8b949e] font-mono">
                        {it.pr_number > 0 && (
                          <a
                            href={it.pr_url}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            className="text-[#58a6ff] hover:underline"
                          >
                            PR #{it.pr_number}
                          </a>
                        )}

                        {it.branch && (
                          <span className="truncate max-w-[140px] text-[#8b949e]">{it.branch}</span>
                        )}

                        {it.retry_count > 0 && (
                          <span className="text-[#d29922]">リトライ: {it.retry_count}</span>
                        )}
                      </div>

                      <div className="flex items-center justify-between pt-1 text-[11px] font-mono text-[#8b949e]">
                        <span>{fmtSince(it.updated_at || it.reconciled_at)}前</span>
                      </div>
                    </div>
                  );
                })
              )}
            </div>

            {hiddenCount > 0 && (
              <div className="px-3 py-1.5 bg-[#0d1117]/30 border-t border-[#30363d] text-center text-xs text-[#8b949e] font-mono">
                他 {hiddenCount} 件は省略している。
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
};
