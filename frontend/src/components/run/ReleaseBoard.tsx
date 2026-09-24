import { useEffect, useMemo, useState } from 'react';
import { listPrintRun, submitRunRelease } from '../../api/print-run';
import { listPressUnit } from '../../api/press-unit';
import { ApiError } from '../../api/client';
import type { DomainRecord } from '../../types/domain';
import { RunStateBadge } from '../common/RunStateBadge';
import type { RunState } from '../../types/status';

// ReleaseBoard is the 复核员放行工作台: every 校样中 batch shows 计划份数、累计
// 数量、剩余数量 in one row and allows recording 本次完成数 with the 印刷机台
// inline. Any failure reason (over-plan, duplicate interval, role) is shown on
// that row and never mutates the displayed cumulative numbers.
export function ReleaseBoard({ canReview }: { canReview: boolean }) {
  const [runs, setRuns] = useState<DomainRecord[]>([]);
  const [presses, setPresses] = useState<DomainRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<Record<number, string>>({});
  const [drafts, setDrafts] = useState<Record<number, { copies: string; pressCode: string }>>({});
  const [busyId, setBusyId] = useState<number | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const [runResult, pressResult] = await Promise.all([listPrintRun(1, 100), listPressUnit(1, 100)]);
      setRuns(runResult.data);
      setPresses(pressResult.data);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void load(); }, []);

  const proofingRuns = useMemo(
    () => runs.filter((item) => item.status === 'proofing').sort((a, b) => (a.remainingCopies ?? 0) - (b.remainingCopies ?? 0)),
    [runs],
  );
  const releasedRuns = useMemo(() => runs.filter((item) => item.status === 'released'), [runs]);

  const updateDraft = (id: number, patch: Partial<{ copies: string; pressCode: string }>) => {
    setDrafts((current) => ({ ...current, [id]: { copies: current[id]?.copies || '', pressCode: current[id]?.pressCode || '', ...patch } }));
  };

  const submit = async (run: DomainRecord) => {
    const draft = drafts[run.id];
    const completedCopies = Number(draft?.copies);
    const pressCode = draft?.pressCode || '';
    if (!draft?.copies || !Number.isInteger(completedCopies) || completedCopies <= 0) {
      setFailure((current) => ({ ...current, [run.id]: '本次完成数必须为正整数。' }));
      return;
    }
    if (completedCopies > (run.remainingCopies ?? 0)) {
      setFailure((current) => ({ ...current, [run.id]: `本次 ${completedCopies} 份超过剩余 ${run.remainingCopies} 份，累计不能超过计划 ${run.plannedCopies} 份。` }));
      return;
    }
    if (!pressCode) {
      setFailure((current) => ({ ...current, [run.id]: '请选择印刷机台。' }));
      return;
    }
    setBusyId(run.id);
    setFailure((current) => { const next = { ...current }; delete next[run.id]; return next; });
    try {
      const result = await submitRunRelease(run.id, { expectedVersion: run.version, completedCopies, pressCode });
      setRuns((current) => current.map((item) => (item.id === run.id ? result.data : item)));
      setDrafts((current) => { const next = { ...current }; delete next[run.id]; return next; });
    } catch (cause) {
      const message = cause instanceof ApiError
        ? `${cause.status === 409 ? '提交冲突：' : ''}${cause.message}`
        : '提交失败，请稍后重试。';
      setFailure((current) => ({ ...current, [run.id]: message }));
      // Refetch so the row reflects the ledger state set by the winning request.
      await load();
    } finally {
      setBusyId(null);
    }
  };

  return <div className="workspace workspace--tight">
  <section className="release-board" aria-busy={loading}>
    <header className="release-board__header">
      <div>
        <span className="eyebrow">PARTIAL RELEASE</span>
        <h2>分批印刷放行</h2>
        <p>印刷常分几次完成。每笔放行填写本次完成数与印刷机台，累计不超过计划份数且区间不重叠；满数自动转为已放行。</p>
      </div>
      <div className="release-board__stats">
        <article><small>校样中批次</small><strong>{proofingRuns.length}</strong></article>
        <article><small>已放行批次</small><strong>{releasedRuns.length}</strong></article>
      </div>
    </header>
    {loading ? <div className="loading">正在加载放行台账…</div> : <div className="release-board__table">
      <table>
        <thead><tr>
          <th>批次</th><th>状态</th><th>计划份数</th><th>累计数量</th><th>剩余数量</th><th>进度</th>
          {canReview && <><th>本次完成数</th><th>印刷机台</th><th>操作</th></>}
        </tr></thead>
        <tbody>
          {proofingRuns.map((run) => {
            const draft = drafts[run.id];
            const percent = run.plannedCopies ? Math.min(100, Math.round(((run.releasedCopies ?? 0) / run.plannedCopies) * 100)) : 0;
            return <tr key={run.id}>
              <td><strong>{run.code}</strong><small>{run.name}</small></td>
              <td><RunStateBadge state={run.status as RunState} /></td>
              <td>{run.plannedCopies ?? 0}</td>
              <td><strong>{run.releasedCopies ?? 0}</strong></td>
              <td><strong className="remaining-pill">{run.remainingCopies ?? 0}</strong></td>
              <td><div className="progress-track progress-track--inline"><span style={{ width: `${percent}%` }} /></div><small>{percent}%</small></td>
              {canReview && <>
                <td><input aria-label="本次完成数" inputMode="numeric" className="copies-input"
                  value={draft?.copies ?? ''} disabled={busyId === run.id}
                  onChange={(event) => updateDraft(run.id, { copies: event.target.value.replace(/[^0-9]/g, '') })}
                  placeholder={`≤ ${run.remainingCopies ?? 0}`} /></td>
                <td><select aria-label="印刷机台" className="press-select" value={draft?.pressCode ?? ''} disabled={busyId === run.id}
                  onChange={(event) => updateDraft(run.id, { pressCode: event.target.value })}>
                  <option value="">选择机台</option>
                  {presses.map((press) => <option key={press.id} value={press.code}>{press.code}</option>)}
                </select></td>
                <td>
                  <button className="table-action" disabled={busyId === run.id} onClick={() => void submit(run)}>
                    {busyId === run.id ? '提交中…' : '登记放行'}
                  </button>
                  {failure[run.id] && <div className="alert alert--inline" role="alert">{failure[run.id]}</div>}
                </td>
              </>}
            </tr>;
          })}
          {releasedRuns.map((run) => <tr key={run.id} className="row--released">
            <td><strong>{run.code}</strong><small>{run.name}</small></td>
            <td><RunStateBadge state={run.status as RunState} /></td>
            <td>{run.plannedCopies ?? 0}</td>
            <td><strong>{run.releasedCopies ?? 0}</strong></td>
            <td><strong className="remaining-pill is-done">0</strong></td>
            <td><div className="progress-track progress-track--inline"><span style={{ width: '100%' }} /></div><small>100%</small></td>
            {canReview && <td colSpan={3}><span className="muted">已满数放行，共 {run.releases?.length ?? 0} 笔记录</span></td>}
          </tr>)}
          {!runs.length && <tr><td colSpan={canReview ? 9 : 6} className="empty">暂无可放行批次</td></tr>}
        </tbody>
      </table>
    </div>}
  </section>
  </div>;
}
