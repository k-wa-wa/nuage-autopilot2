export function pad(n: number): string {
  return String(n).padStart(2, '0');
}

export function fmtTime(dateStr?: string | null): string {
  if (!dateStr) return '-';
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return '-';
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export function fmtDuration(ms: number): string {
  if (!isFinite(ms) || ms < 0) return '-';
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}分${pad(s % 60)}秒`;
  return `${Math.floor(m / 60)}時間${pad(m % 60)}分`;
}

export function fmtSince(dateStr?: string | null): string {
  if (!dateStr) return '-';
  const time = new Date(dateStr).getTime();
  if (isNaN(time)) return '-';
  return fmtDuration(Date.now() - time);
}

export function fmtBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}
