// Go 側の web パッケージの型と 1:1 に対応する TypeScript 型定義

export interface AgentInfo {
  use: string;
  command: string;
  model: string;
  timeout: string;
}

export interface Meta {
  login: string;
  project_owner: string;
  project_number: number;
  repos: string[];
  statuses: string[];
  agents: AgentInfo[];
}

export interface Active {
  run_id: number;
  phase: string;
  repo: string;
  issue: number;
  started_at: string;
  agent_started_at: string;
}

export interface RunView {
  id: number;
  repo: string;
  issue: number;
  phase: string;
  started_at: string | null;
  ended_at: string | null;
  result: string; // 'ok' | 'fail' | etc.
  has_log: boolean;
  running: boolean;
}

export interface ItemView {
  repo: string;
  issue: number;
  status: string;
  pr_number: number;
  branch: string;
  retry_count: number;
  lease_until: string | null;
  verify_since: string | null;
  terminal: boolean;
  updated_at: string | null;
  reconciled_at: string | null;
  issue_url: string;
  pr_url?: string;
  last_run?: RunView | null;
  running: boolean;
}

export interface StateResponse {
  generated_at: string;
  meta: Meta;
  queue_depth: number;
  active: Active | null;
  active_has_log: boolean;
  items: ItemView[];
}

export interface ItemResponse {
  item: ItemView;
  runs: RunView[];
}

export interface LogView {
  header: string;
  prompt: string;
  prompt_truncated: boolean;
  output: string;
  output_truncated: boolean;
  size: number;
}

export interface RunResponse {
  run: RunView;
  log?: LogView;
  log_error?: string;
}

// 人間がやるべきことの定期サマリ

export type Urgency = 'high' | 'medium' | 'low';

export interface SummaryTodo {
  repo: string;
  issue: number;
  title: string;
  status: string;
  urgency: Urgency;
  why: string;
  action: string;
}

export interface SummaryReport {
  headline: string;
  todos: SummaryTodo[] | null;
  notes: string;
}

export interface SummaryView {
  id: number;
  created_at: string;
  run_id: number;
  // report は出力を JSON として解釈できた場合のみ入る。読めなかった場合は raw に生の出力が入る。
  report: SummaryReport | null;
  raw: string;
}

export interface SummaryMeta {
  id: number;
  created_at: string;
  headline: string;
  todo_count: number;
}

export interface SummaryResponse {
  // schedule が空文字なら定期生成は無効。
  schedule: string;
  next_at: string | null;
  current: SummaryView | null;
  history: SummaryMeta[];
}

export interface LogChunkResponse {
  data: string;
  next: number;
  size: number;
  skipped: boolean;
  running: boolean;
}
