import { useEffect, useMemo, useState } from 'react';
import { listPressUnit } from '../../api/press-unit';
import { submitRunRelease } from '../../api/print-run';
import { ApiError, request } from '../../api/client';
import type { DomainRecord } from '../../types/domain';
import { formatDate } from '../../utils/format';

// PartialReleaseDialog 让复核员登记一次分批放行：填写本次完成数与印刷机台，
// 弹窗内始终展示计划/累计/剩余；提交失败（超计划、区间冲突）时只显示失败
// 原因，并以后端最新台账刷新累计数量，失败提交不改变任何数字。
export function PartialReleaseDialog({ open, run, onClose, onSubmitted, onRefresh }: {
  open: boolean;
  run: DomainRecord | null;
  onClose: () => void;
  onSubmitted: (updated: DomainRecord) => void;
  onRefresh: (updated: DomainRecord) => void;
}) {
  const [copies, setCopies] = useState('');
  const [pressCode, setPressCode] = useState('');
  const [reason, setReason] = useState('');
  const [presses, setPresses] = useState<DomainRecord[]>([]);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) return;
    setCopies(''); setPressCode(''); setReason(''); setError(''); setSubmitting(false);
    void listPressUnit(1, 100).then((result: { data: DomainRecord[] }) => setPresses(result.data));
  }, [open, run?.id]);

  const planned = run?.plannedCopies ?? 0;
  const released = run?.releasedCopies ?? 0;
  const remaining = run?.remainingCopies ?? planned - released;
  const parsedCopies = useMemo(() => {
    const value = Number(copies);
    return copies !== '' && Number.isInteger(value) && value > 0 ? value : 0;
  }, [copies]);
  const projectedRemaining = remaining - parsedCopies;

  if (!open || !run) return null;

  const submit = async () => {
    setError('');
    if (parsedCopies <= 0) {
      setError('本次完成数必须为正整数。');
      return;
    }
    if (parsedCopies > remaining) {
      setError(`本次完成数 ${parsedCopies} 份超过剩余 ${remaining} 份，累计不能超过计划 ${planned} 份。`);
      return;
    }
    if (!pressCode) {
      setError('请选择印刷机台。');
      return;
    }
    setSubmitting(true);
    try {
      const result = await submitRunRelease(run.id, {
        expectedVersion: run.version,
        completedCopies: parsedCopies,
        pressCode,
        reason: reason.trim() || undefined,
      });
      onSubmitted(result.data);
    } catch (cause) {
      const message = cause instanceof ApiError
        ? `${cause.status === 409 ? '同一区间已有放行先提交成功（只保留一笔）：' : ''}${cause.message}`
        : '提交失败，请稍后重试。';
      setError(message);
      setCopies('');
      // 失败后以后端最新台账刷新累计/剩余与版本，保证重试不再基于旧数据。
      try {
        const latest = await request<DomainRecord>(`/runs/${run.id}`);
        onRefresh(latest.data);
      } catch {
        // 刷新失败时保留当前视图与错误提示。
      }
    } finally {
      setSubmitting(false);
    }
  };

  return <div className="modal-backdrop">
    <section className="modal modal--wide" role="dialog" aria-modal="true">
      <h2>分批放行 · {run.code}</h2>
      <p className="muted">{run.name} · {run.facility}</p>
      <div className="release-ledger">
        <article><small>计划份数</small><strong>{planned}</strong></article>
        <article><small>累计放行</small><strong>{released}</strong></article>
        <article><small>剩余数量</small><strong className={remaining === 0 ? 'is-done' : ''}>{remaining}</strong></article>
      </div>
      <div className="progress-track" aria-label="放行进度">
        <span style={{ width: `${planned > 0 ? Math.min(100, (released / planned) * 100) : 0}%` }} />
      </div>
      <label className="form-field">
        <span>本次完成数（份）</span>
        <input aria-label="本次完成数" inputMode="numeric" value={copies}
          onChange={(event) => setCopies(event.target.value.replace(/[^0-9]/g, ''))}
          placeholder={`最多 ${remaining} 份`} />
        {parsedCopies > 0 && <small className={projectedRemaining < 0 ? 'form-error' : 'form-hint'}>
          提交后累计 {released + parsedCopies} / {planned}，剩余 {Math.max(0, projectedRemaining)}
          {projectedRemaining === 0 ? '，本批将转为已放行' : ''}
        </small>}
      </label>
      <label className="form-field">
        <span>印刷机台</span>
        <select aria-label="印刷机台" value={pressCode} onChange={(event) => setPressCode(event.target.value)}>
          <option value="">请选择机台</option>
          {presses.map((press) => <option key={press.id} value={press.code}>{press.code} · {press.name}（{press.status}）</option>)}
        </select>
      </label>
      <label className="form-field">
        <span>放行说明（可选）</span>
        <input aria-label="放行说明" value={reason} maxLength={500}
          onChange={(event) => setReason(event.target.value)} placeholder="例如：夜班补印 2400 份，色差复核通过" />
      </label>
      {run.releases?.length ? <div className="ledger-history">
        <h3>已有放行记录（{run.releases.length} 笔）</h3>
        {run.releases.slice(-3).map((entry) => <article key={entry.id}>
          <strong>第 {entry.startSequence}–{entry.endSequence} 份</strong>
          <span>{entry.completedCopies} 份 · {entry.pressCode} · {entry.actor}</span>
          <time>{formatDate(entry.createdAt)}</time>
        </article>)}
      </div> : null}
      {error && <div className="alert" role="alert">{error}</div>}
      <footer>
        <button className="link-button" onClick={onClose} disabled={submitting}>取消</button>
        <button className="primary-button" onClick={() => void submit()} disabled={submitting || remaining === 0}>
          {submitting ? '提交中…' : '登记放行'}
        </button>
      </footer>
    </section>
  </div>;
}
