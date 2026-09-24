import { useEffect, useState } from 'react';
import { createRunRelease, listRunReleases } from '../api/print-run';
import type { DomainRecord, RunReleaseRecord } from '../types/domain';
import { formatDate } from '../utils/format';
import { UiButton } from './common/UiButton';

// RunReleaseDialog 是复核员的分批放行面板：登记本次完成数和印刷机台，
// 实时展示累计数量、剩余数量、历史放行区间以及上一次失败原因。
export function RunReleaseDialog({ run, onClose }: { run: DomainRecord; onClose: (changed: boolean) => void }) {
  const [releases, setReleases] = useState<RunReleaseRecord[]>([]);
  const [released, setReleased] = useState(run.releasedQuantity);
  const [runStatus, setRunStatus] = useState(run.status);
  const [quantity, setQuantity] = useState('');
  const [pressUnit, setPressUnit] = useState('');
  const [note, setNote] = useState('');
  const [error, setError] = useState('');
  const [changed, setChanged] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const planned = run.plannedQuantity;
  const remaining = planned - released;

  useEffect(() => {
    listRunReleases(run.id).then((result) => setReleases(result.data)).catch(() => setReleases([]));
  }, [run.id]);

  const submit = async () => {
    const qty = Number(quantity);
    if (!Number.isInteger(qty) || qty < 1) { setError('本次完成数须为不小于 1 的整数'); return; }
    if (pressUnit.trim().length < 2) { setError('请填写印刷机台'); return; }
    setSubmitting(true);
    setError('');
    try {
      const result = await createRunRelease(run.id, { quantity: qty, pressUnit: pressUnit.trim(), note: note.trim() });
      setReleased(result.data.releasedQuantity);
      setRunStatus(result.data.run.status);
      setReleases((previous) => [...previous, result.data.release]);
      setQuantity('');
      setNote('');
      setChanged(true);
    } catch (cause) {
      // 失败的提交不会改变累计数量，只在这里展示失败原因。
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return <div className="modal-backdrop"><section className="modal modal--wide" role="dialog" aria-modal="true">
    <h2>分批放行 · {run.code}</h2>
    <div className="release-summary">
      <div><span>计划份数</span><strong>{planned}</strong></div>
      <div><span>累计数量</span><strong>{released}</strong></div>
      <div><span>剩余数量</span><strong>{remaining}</strong></div>
    </div>
    {runStatus === 'released'
      ? <p className="release-done">已满数，批次状态转为已放行。</p>
      : <div className="release-form">
          <label>本次完成数<input aria-label="本次完成数" type="number" min={1} max={remaining} value={quantity} onChange={(event) => setQuantity(event.target.value)} /></label>
          <label>印刷机台<input aria-label="印刷机台" placeholder="如 PU-001" value={pressUnit} onChange={(event) => setPressUnit(event.target.value)} /></label>
          <label>备注<input aria-label="备注" placeholder="选填" value={note} onChange={(event) => setNote(event.target.value)} /></label>
        </div>}
    {error && <div className="alert" role="alert">{error}</div>}
    {releases.length > 0 && <div className="release-list"><h3>放行记录</h3>
      {releases.map((release) => <article key={release.id}>
        <strong>第 {release.startNo}–{release.endNo} 份 · {release.quantity} 份</strong>
        <span>{release.pressUnit} · {release.actor} · {formatDate(release.createdAt)}</span>
        {release.note && <span>{release.note}</span>}
      </article>)}
    </div>}
    <footer>
      <button className="link-button" onClick={() => onClose(changed)}>关闭</button>
      {runStatus !== 'released' && <UiButton onClick={() => void submit()} disabled={submitting}>登记放行</UiButton>}
    </footer>
  </section></div>;
}
