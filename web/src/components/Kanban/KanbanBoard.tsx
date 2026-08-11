import React from 'react';
import type { ItemView } from '../../types/api';
import { ItemCard } from './ItemCard';
import { StatusBadge } from '../StatusBadge/StatusBadge';

interface KanbanBoardProps {
  statuses: string[];
  items: ItemView[];
  onSelectItem: (item: ItemView) => void;
  onSelectRun?: (runId: number) => void;
}

export const KanbanBoard: React.FC<KanbanBoardProps> = ({
  statuses,
  items,
  onSelectItem,
  onSelectRun,
}) => {
  // ステータスごとにアイテムをグループ化
  const groupedItems = React.useMemo(() => {
    const map: Record<string, ItemView[]> = {};
    for (const st of statuses) {
      map[st] = [];
    }
    for (const it of items) {
      if (!map[it.status]) {
        map[it.status] = [];
      }
      map[it.status].push(it);
    }
    return map;
  }, [statuses, items]);

  return (
    <div className="flex gap-4 overflow-x-auto pb-6 pt-2 select-none">
      {statuses.map((status) => {
        const laneItems = groupedItems[status] || [];
        const runningCount = laneItems.filter((it) => it.running).length;

        return (
          <div
            key={status}
            className="flex-shrink-0 w-72 sm:w-80 bg-slate-900/40 border border-slate-800/80 rounded-2xl flex flex-col max-h-[calc(100vh-14rem)] backdrop-blur-sm"
          >
            {/* レーンヘッダー */}
            <div className="p-3.5 border-b border-slate-800/80 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <StatusBadge status={status} />
                <span className="text-xs font-mono text-slate-500">
                  {laneItems.length}
                </span>
              </div>

              {runningCount > 0 && (
                <span className="w-2 h-2 rounded-full bg-amber-400 animate-ping" title="実行中" />
              )}
            </div>

            {/* アイテムカード一覧 */}
            <div className="p-2.5 space-y-2.5 overflow-y-auto flex-1 custom-scrollbar">
              {laneItems.length === 0 ? (
                <div className="py-8 text-center text-xs text-slate-600 font-mono">
                  アイテムなし
                </div>
              ) : (
                laneItems.map((item) => (
                  <ItemCard
                    key={`${item.repo}#${item.issue}`}
                    item={item}
                    onSelect={onSelectItem}
                    onSelectRun={onSelectRun}
                  />
                ))
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
};
