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

export interface LogChunkResponse {
  data: string;
  next: number;
  size: number;
  skipped: boolean;
  running: boolean;
}
