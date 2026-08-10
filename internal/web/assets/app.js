"use strict";

// autopilot の状態を参照する画面。
//
// 更新系は一切持たない。表示は SQLite のキャッシュとエージェントのログだけを源にする。
// ルーティングはハッシュで行い、サーバ側にパスを持たせない。

// LANE_LIMIT は 1 レーンに表示する最大件数。
const LANE_LIMIT = 50;

const view = document.getElementById("view");
const metaEl = document.getElementById("meta");
const stampEl = document.getElementById("stamp");

// timers は画面遷移のたびに全部止める。前の画面のポーリングが残ると二重に走る。
let timers = [];

function clearTimers() {
  timers.forEach(clearInterval);
  timers = [];
}

function every(ms, fn) {
  timers.push(setInterval(fn, ms));
}

async function getJSON(path) {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body;
}

const el = (tag, props = {}, children = []) => {
  const n = Object.assign(document.createElement(tag), props);
  for (const c of [].concat(children)) {
    if (c === null || c === undefined || c === false) continue;
    n.append(c);
  }
  return n;
};

// ---- 表示の整形 -------------------------------------------------------------

const pad = (n) => String(n).padStart(2, "0");

function fmtTime(s) {
  if (!s) return "-";
  const d = new Date(s);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes(),
  )}:${pad(d.getSeconds())}`;
}

function fmtDuration(ms) {
  if (!isFinite(ms) || ms < 0) return "-";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}分${pad(s % 60)}秒`;
  return `${Math.floor(m / 60)}時間${pad(m % 60)}分`;
}

function fmtSince(s) {
  return s ? fmtDuration(Date.now() - new Date(s).getTime()) : "-";
}

function fmtBytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

// runBadge は実行の結果を一目で分かる形にする。
function runBadge(run) {
  if (!run) return el("span", { className: "muted", textContent: "-" });
  if (run.running) return el("span", { className: "badge running", textContent: "実行中" });
  if (!run.ended_at) return el("span", { className: "badge err", textContent: "中断" });
  if (run.result === "ok") return el("span", { className: "badge ok", textContent: "ok" });
  return el("span", { className: "badge err", textContent: "失敗" });
}

const itemHash = (repo, issue) => `#/item/${encodeURIComponent(repo)}/${issue}`;
const runHash = (id) => `#/run/${id}`;

// ---- ダッシュボード ---------------------------------------------------------

function renderActive(state) {
  const panel = el("section", { className: "panel active-card" }, [
    el("h2", { textContent: "実行中のエージェント" }),
  ]);
  const a = state.active;
  if (!a) {
    panel.append(
      el("p", {
        className: "idle",
        textContent:
          state.queue_depth > 0
            ? `待機中（キューに ${state.queue_depth} 件）`
            : "待機中。実行中のエージェントはない。",
      }),
    );
    return panel;
  }

  panel.append(
    el("div", { className: "active-head" }, [
      el("span", { className: "badge phase", textContent: a.phase }),
      el("a", { className: "issue", href: itemHash(a.repo, a.issue), textContent: `${a.repo} #${a.issue}` }),
      el("span", { className: "badge running", textContent: "実行中" }),
    ]),
  );

  const dl = el("dl", { className: "kv" });
  const row = (k, v) => {
    dl.append(el("dt", { textContent: k }), el("dd", {}, [v]));
  };
  row("ジョブ開始", `${fmtTime(a.started_at)}（${fmtSince(a.started_at)}前）`);
  row(
    "プロセス起動",
    a.agent_started_at
      ? `${fmtTime(a.agent_started_at)}（${fmtSince(a.agent_started_at)}前）`
      : "準備中（ワークツリー同期・プロンプト生成）",
  );
  row("キュー", `${state.queue_depth} 件待ち`);
  panel.append(dl);

  if (state.active_has_log) {
    panel.append(
      el("p", { className: "note" }, [
        el("a", { href: runHash(a.run_id), textContent: "プロンプトと出力を見る →" }),
      ]),
    );
  } else {
    panel.append(el("p", { className: "note", textContent: "ログはまだ開かれていない。" }));
  }
  return panel;
}

function itemRow(it) {
  const tr = el("tr", { className: it.running ? "is-running" : "" });
  tr.append(
    el("td", {}, [
      el("a", { href: itemHash(it.repo, it.issue), textContent: `#${it.issue}` }),
      el("span", { className: "muted mono", textContent: ` ${it.repo}` }),
    ]),
    el("td", {}, [
      it.pr_number
        ? el("a", { href: it.pr_url, target: "_blank", rel: "noreferrer", textContent: `#${it.pr_number}` })
        : el("span", { className: "muted", textContent: "-" }),
    ]),
    el("td", { className: "mono" }, [
      it.branch || el("span", { className: "muted", textContent: "-" }),
    ]),
    el("td", { className: "num" }, [
      it.retry_count ? String(it.retry_count) : el("span", { className: "muted", textContent: "0" }),
    ]),
    el("td", {}, [
      it.last_run
        ? el("a", { href: runHash(it.last_run.id) }, [
            el("span", { className: "badge phase", textContent: it.last_run.phase }),
          ])
        : el("span", { className: "muted", textContent: "-" }),
    ]),
    el("td", {}, [runBadge(it.last_run)]),
    el("td", { className: "muted mono", textContent: fmtTime(it.updated_at) }),
  );
  return tr;
}

function renderLanes(state) {
  const frag = document.createDocumentFragment();
  const byStatus = new Map();
  for (const it of state.items) {
    if (!byStatus.has(it.status)) byStatus.set(it.status, []);
    byStatus.get(it.status).push(it);
  }

  // 設定されたレーン順を先に、未知の Status を後ろに並べる。
  const order = state.meta.statuses.slice();
  for (const s of byStatus.keys()) if (!order.includes(s)) order.push(s);

  for (const status of order) {
    const all = byStatus.get(status);
    if (!all || all.length === 0) continue;
    all.sort((a, b) => new Date(b.updated_at || 0) - new Date(a.updated_at || 0));
    // items は終端になっても消えないため Done は際限なく伸びる。
    // 新しい順に頭打ちにして、残りは件数だけ知らせる。
    const items = all.slice(0, LANE_LIMIT);
    const hidden = all.length - items.length;

    const table = el("table", {}, [
      el("thead", {}, [
        el("tr", {}, [
          el("th", { textContent: "Issue" }),
          el("th", { textContent: "PR" }),
          el("th", { textContent: "ブランチ" }),
          el("th", { textContent: "リトライ" }),
          el("th", { textContent: "直近フェーズ" }),
          el("th", { textContent: "結果" }),
          el("th", { textContent: "更新" }),
        ]),
      ]),
    ]);
    const tbody = el("tbody");
    items.forEach((it) => tbody.append(itemRow(it)));
    table.append(tbody);

    frag.append(
      el("section", { className: "panel" }, [
        el("h3", { className: "lane-title" }, [
          document.createTextNode(status || "(Status 未設定)"),
          el("span", { className: "count", textContent: `${all.length} 件` }),
        ]),
        table,
        hidden ? el("p", { className: "note", textContent: `他 ${hidden} 件は省略している。` }) : null,
      ]),
    );
  }

  if (!frag.childNodes.length) {
    frag.append(
      el("section", { className: "panel" }, [
        el("p", { className: "empty", textContent: "ローカルに記録された Issue はまだない。" }),
      ]),
    );
  }
  return frag;
}

function renderMeta(meta) {
  metaEl.textContent = `${meta.project_owner}/${meta.project_number}・${meta.repos.join(", ")}・${meta.login}`;
}

async function showDashboard() {
  const load = async () => {
    const state = await getJSON("/api/state");
    renderMeta(state.meta);
    stampEl.textContent = `更新 ${fmtTime(state.generated_at)}`;
    view.replaceChildren(renderActive(state), renderLanes(state));
  };
  await load();
  every(5000, () => load().catch(showError));
}

// ---- Issue 詳細 -------------------------------------------------------------

async function showItem(repo, issue) {
  const load = async () => {
    const data = await getJSON(`/api/item?repo=${encodeURIComponent(repo)}&issue=${issue}`);
    const it = data.item;
    stampEl.textContent = `更新 ${fmtTime(new Date().toISOString())}`;

    const dl = el("dl", { className: "kv" });
    const row = (k, v) => dl.append(el("dt", { textContent: k }), el("dd", {}, [v]));
    row("Status", it.status || "-");
    row("Issue", el("a", { href: it.issue_url, target: "_blank", rel: "noreferrer", textContent: it.issue_url }));
    row(
      "PR",
      it.pr_number
        ? el("a", { href: it.pr_url, target: "_blank", rel: "noreferrer", textContent: `#${it.pr_number}` })
        : "-",
    );
    row("ブランチ", el("span", { className: "mono", textContent: it.branch || "-" }));
    row("リトライ", String(it.retry_count));
    row("lease 期限", fmtTime(it.lease_until));
    row("Verifying 開始", fmtTime(it.verify_since));
    row("終端", it.terminal ? "はい" : "いいえ");
    row("ローカル更新", fmtTime(it.updated_at));

    const tbody = el("tbody");
    for (const run of data.runs) {
      tbody.append(
        el("tr", { className: run.running ? "is-running" : "" }, [
          el("td", {}, [
            run.has_log
              ? el("a", { href: runHash(run.id), textContent: run.phase })
              : el("span", { textContent: run.phase }),
          ]),
          el("td", {}, [runBadge(run)]),
          el("td", { className: "muted mono", textContent: fmtTime(run.started_at) }),
          el("td", { className: "muted", textContent: run.ended_at
            ? fmtDuration(new Date(run.ended_at) - new Date(run.started_at))
            : run.running
              ? fmtSince(run.started_at)
              : "-" }),
          el("td", { className: "mono", textContent: run.result === "ok" ? "" : run.result }),
        ]),
      );
    }

    view.replaceChildren(
      el("a", { className: "back", href: "#/", textContent: "← 一覧へ" }),
      el("section", { className: "panel" }, [
        el("h2", { textContent: `${it.repo} #${it.issue}` }),
        dl,
      ]),
      el("section", { className: "panel" }, [
        el("h2", { textContent: `実行履歴（${data.runs.length} 件）` }),
        data.runs.length
          ? el("table", {}, [
              el("thead", {}, [
                el("tr", {}, [
                  el("th", { textContent: "フェーズ" }),
                  el("th", { textContent: "結果" }),
                  el("th", { textContent: "開始" }),
                  el("th", { textContent: "所要" }),
                  el("th", { textContent: "詳細" }),
                ]),
              ]),
              tbody,
            ])
          : el("p", { className: "empty", textContent: "実行履歴はまだない。" }),
      ]),
    );
  };
  await load();
  every(5000, () => load().catch(showError));
}

// ---- 実行の詳細（プロンプトと出力） -----------------------------------------

async function showRun(id) {
  const data = await getJSON(`/api/run?id=${id}`);
  const run = data.run;

  const outPre = el("pre", { className: "out" });
  const notes = el("div");

  const head = el("section", { className: "panel" }, [
    el("div", { className: "active-head" }, [
      el("span", { className: "badge phase", textContent: run.phase }),
      el("a", {
        className: "issue",
        href: itemHash(run.repo, run.issue),
        textContent: `${run.repo} #${run.issue}`,
      }),
      runBadge(run),
    ]),
    el("dl", { className: "kv" }, []),
  ]);
  const dl = head.querySelector("dl");
  const row = (k, v) => dl.append(el("dt", { textContent: k }), el("dd", {}, [v]));
  row("開始", fmtTime(run.started_at));
  row(
    "終了",
    run.ended_at ? `${fmtTime(run.ended_at)}（所要 ${fmtDuration(new Date(run.ended_at) - new Date(run.started_at))}）`
      : run.running ? `実行中（${fmtSince(run.started_at)}経過）`
      : "記録なし（ワーカーが異常終了した可能性）",
  );
  if (run.result && run.result !== "ok") row("結果", el("span", { className: "mono", textContent: run.result }));

  view.replaceChildren(el("a", { className: "back", href: itemHash(run.repo, run.issue), textContent: "← Issue へ" }), head);

  if (data.log_error) {
    view.append(el("section", { className: "panel" }, [el("p", { className: "error", textContent: data.log_error })]));
    return;
  }
  if (!data.log) {
    view.append(
      el("section", { className: "panel" }, [
        el("p", { className: "empty", textContent: "この実行にはログがない。" }),
      ]),
    );
    return;
  }

  const promptPanel = el("section", { className: "panel" }, [
    el("h2", { textContent: "プロンプト" }),
    data.log.header ? el("p", { className: "note mono", textContent: data.log.header }) : null,
    data.log.prompt_truncated
      ? el("p", { className: "warn", textContent: "プロンプトが長いため先頭のみ表示している。" })
      : null,
    el("pre", { className: "out", textContent: data.log.prompt || "(なし)" }),
  ]);

  const outPanel = el("section", { className: "panel" }, [
    el("h2", { textContent: "出力" }),
    notes,
    outPre,
  ]);
  view.append(promptPanel, outPanel);

  outPre.textContent = data.log.output;
  let offset = data.log.size;

  const setNotes = (extra) => {
    notes.replaceChildren();
    if (data.log.output_truncated) {
      notes.append(el("p", { className: "warn", textContent: "出力が大きいため末尾のみ表示している。" }));
    }
    if (run.running) {
      // print モードのエージェント CLI は完了まで出力しない。空欄の理由を明示する。
      notes.append(
        el("p", {
          className: "note",
          textContent:
            "実行中。エージェント CLI は非対話モードでは完了時にまとめて出力するため、終わるまでここは空のことが多い。",
        }),
      );
    }
    if (extra) notes.append(el("p", { className: "note", textContent: extra }));
    notes.append(el("p", { className: "note", textContent: `ログ ${fmtBytes(offset)}` }));
  };
  setNotes();

  if (!run.running) return;

  // 実行中は末尾を追いかける。追記が無い間はサイズ表示だけが更新される。
  every(2000, async () => {
    try {
      const chunk = await getJSON(`/api/run/log?id=${id}&offset=${offset}`);
      offset = chunk.next;
      if (chunk.data) {
        const atBottom = outPre.scrollHeight - outPre.scrollTop - outPre.clientHeight < 40;
        outPre.append(chunk.skipped ? `\n…（途中を省略）\n${chunk.data}` : chunk.data);
        if (atBottom) outPre.scrollTop = outPre.scrollHeight;
      }
      setNotes();
      // 実行が終わったら結果と所要時間を反映するために描き直す。
      // 追記の応答に running が入っているので、そのための問い合わせは要らない。
      if (!chunk.running) {
        clearTimers();
        route();
      }
    } catch (e) {
      setNotes(`追従に失敗: ${e.message}`);
    }
  });
}

// ---- ルーティング -----------------------------------------------------------

function showError(err) {
  view.replaceChildren(el("section", { className: "panel" }, [el("p", { className: "error", textContent: String(err.message || err) })]));
}

function route() {
  clearTimers();
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const done = (p) => p.catch(showError);

  if (parts[0] === "item" && parts.length >= 3) {
    done(showItem(decodeURIComponent(parts[1]), Number(parts[2])));
    return;
  }
  if (parts[0] === "run" && parts.length >= 2) {
    done(showRun(Number(parts[1])));
    return;
  }
  done(showDashboard());
}

window.addEventListener("hashchange", route);
route();
