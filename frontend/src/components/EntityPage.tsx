import { useEffect, useMemo, useState } from 'react';
import { request } from '../api/client';
import { roleAtLeast, useAuth } from '../hooks/useAuth';
import { usePagination } from '../hooks/usePagination';
import type { EntityConfig, DomainRecord } from '../types/domain';
import type { RunState } from '../types/status';
import type { EntityStore } from '../stores/factory';
import { formatDate } from '../utils/format';
import { StatusBadge } from './common/StatusBadge';
import { RunStateBadge } from './common/RunStateBadge';
import { ColorTable } from './common/ColorTable';
import { EmptyState } from './common/EmptyState';
import { MetricCard } from './common/MetricCard';
import { ConfirmDialog } from './common/ConfirmDialog';
import { UiButton } from './common/UiButton';
import { PartialReleaseDialog } from './run/PartialReleaseDialog';

function decisionRunState(status: string): RunState {
  if (status === 'release') return 'released';
  if (status === 'rework' || status === 'quarantine') return 'hold';
  return 'proofing';
}

function nextPermittedStatus(config: EntityConfig, current: string, reviewer: boolean): string | null {
  const transitions: Record<string, Record<string, string | null>> = {
    pressUnit: { ready: 'setup', setup: 'printing', printing: 'maintenance', maintenance: 'printing' },
    // proofing -> released is intentionally absent: 批次满数放行由分批放行台账
    // 驱动，复核员在同一行使用「分批放行」。
    printRun: { setup: 'printing', printing: 'proofing', proofing: 'hold', hold: 'proofing', released: reviewer ? 'hold' : null },
    colorProof: { captured: 'review', review: reviewer ? 'accepted' : null, accepted: reviewer ? 'review' : null, rejected: reviewer ? 'review' : null },
    releaseDecision: { draft: reviewer ? 'release' : 'rework', release: reviewer ? 'rework' : null, rework: reviewer ? 'release' : null, quarantine: reviewer ? 'rework' : null },
  };
  return transitions[config.key]?.[current] ?? null;
}

export function EntityPage({ config, useStore }: { config: EntityConfig; useStore: EntityStore }) {
  const { session } = useAuth();
  const { items, meta, loading, error, load, createRecord, transition } = useStore();
  const [search, setSearch] = useState('');
  const [submittedSearch, setSubmittedSearch] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [pending, setPending] = useState<{ item: DomainRecord; status: string } | null>(null);
  const [detail, setDetail] = useState<DomainRecord | null>(null);
  const [releaseTarget, setReleaseTarget] = useState<DomainRecord | null>(null);
  const { page, pageSize, pages, setPage, previous, next } = usePagination(meta.total);
  const canWrite = roleAtLeast(session?.role, 'operator');
  const canReview = roleAtLeast(session?.role, 'reviewer');
  const isPrintRun = config.key === 'printRun';
  const columnCount = isPrintRun ? 9 : 8;

  useEffect(() => { void load(config.path, submittedSearch, page, pageSize); }, [config.path, load, page, pageSize, submittedSearch]);
  const highRisk = useMemo(() => items.filter((item) => ['high', 'critical'].includes(item.riskLevel)).length, [items]);
  const createDemo = async () => {
    await createRecord(config.path, { code: `${config.key.toUpperCase()}-${Date.now().toString().slice(-6)}`, name: `新增${config.label}`,
      description: '通过前端工作台创建的业务记录', facility: '默认作业区', owner: session?.username || 'operator', category: '常规', riskLevel: 'medium',
      metricValue: 2.4, metricUnit: 'ΔE', effectiveAt: new Date().toISOString(), evidence: '已完成创建前色彩检查', relatedCode: 'PR-001',
      ...(isPrintRun ? { plannedCopies: 5000 } : {}) });
    setShowCreate(false);
  };
  const openDetail = async (item: DomainRecord) => {
    try { setDetail((await request<DomainRecord>(`/${config.path}/${item.id}`)).data); }
    catch { setDetail(item); }
  };
  const refreshDetail = (updated: DomainRecord) => {
    setDetail(updated);
    setReleaseTarget(null);
    void load(config.path, submittedSearch, page, pageSize);
  };

  return <main className="workspace">
    <header className="page-header"><div><p className="eyebrow">业务工作台</p><h1>{config.label}</h1><p>统一管理{config.label}的状态、风险、证据与责任人。</p></div>{canWrite && <UiButton onClick={() => setShowCreate(true)}>新增{config.label}</UiButton>}</header>
    <section className="metrics"><MetricCard label="记录总数" value={meta.total} detail="当前筛选范围"/><MetricCard label="高风险" value={highRisk} detail="需要优先复核"/><MetricCard label="状态种类" value={new Set(items.map((item) => item.status)).size} detail="状态机覆盖"/></section>
    {(config.key === 'colorProof' || config.key === 'releaseDecision') && <ColorTable records={items} title={config.key === 'colorProof' ? '当前校样读数' : '放行依据读数'} />}
    <section className="toolbar"><input aria-label="搜索" placeholder={`搜索${config.label}编码或名称`} value={search} onChange={(event) => setSearch(event.target.value)} /><UiButton onClick={() => { setPage(1); setSubmittedSearch(search); }}>查询</UiButton><button className="link-button" onClick={() => { setSearch(''); setSubmittedSearch(''); setPage(1); }}>重置</button></section>
    {error && <div className="alert" role="alert">{error}</div>}
    <section className="table-shell" aria-busy={loading}><table><thead><tr><th>编码</th><th>名称</th><th>状态</th><th>风险</th><th>责任人</th><th>指标</th>{isPrintRun && <th>计划 / 累计 / 剩余</th>}<th>更新时间</th><th>操作</th></tr></thead><tbody>
      {items.map((item) => { const target = nextPermittedStatus(config, item.status, canReview); return <tr key={item.id}><td><strong>{item.code}</strong></td><td><button className="record-link" onClick={() => void openDetail(item)}>{item.name}</button><small>{item.facility}</small></td><td>{isPrintRun ? <RunStateBadge state={item.status as RunState}/> : <StatusBadge status={item.status}/>} {config.key === 'releaseDecision' && <RunStateBadge state={decisionRunState(item.status)}/>}</td><td>{item.riskLevel}</td><td>{item.owner}</td><td>{item.metricValue} {item.metricUnit}</td>{isPrintRun && <td><span className="copies-cell">{item.plannedCopies ?? 0} / <strong>{item.releasedCopies ?? 0}</strong> / <em className={item.remainingCopies === 0 ? 'is-done' : ''}>{item.remainingCopies ?? 0}</em></span>{item.status === 'proofing' && <small>剩余 {item.remainingCopies ?? 0} 份待放行</small>}</td>}<td>{formatDate(item.updatedAt)}</td><td className="actions-cell">{canReview && isPrintRun && item.status === 'proofing' && <button className="table-action table-action--primary" onClick={() => setReleaseTarget(item)}>分批放行</button>}{canWrite && target ? <button className="table-action" onClick={() => setPending({ item, status: target })}>推进至 {target}</button> : (!canReview || !(isPrintRun && item.status === 'proofing')) && <button className="table-action" onClick={() => void openDetail(item)}>查看详情</button>}</td></tr>; })}
      {!items.length && !loading && <tr><td colSpan={columnCount}><EmptyState title="没有匹配记录" detail="可清空搜索条件后重新查询" /></td></tr>}
    </tbody></table>{loading && <div className="loading">正在同步业务数据…</div>}</section>
    <footer className="pagination"><button onClick={previous} disabled={page <= 1}>上一页</button><span>第 {page} / {pages} 页</span><button onClick={next} disabled={page >= pages}>下一页</button></footer>
    <ConfirmDialog open={showCreate} title={`新增${config.label}`} onCancel={() => setShowCreate(false)} onConfirm={() => void createDemo()}><p>将创建一条包含完整责任人、风险和证据信息的演示记录{isPrintRun ? '，计划份数 5000' : ''}。</p></ConfirmDialog>
    <ConfirmDialog open={Boolean(pending)} title="确认状态迁移" onCancel={() => setPending(null)} onConfirm={() => { if (pending) void transition(config.path, pending.item, pending.status).then(() => setPending(null)); }}><p>状态迁移会写入审计日志；色彩配置和放行决定同时生成不可变版本。</p><strong>{pending?.item.status} → {pending?.status}</strong></ConfirmDialog>
    <ConfirmDialog open={Boolean(detail)} title={`${detail?.code || ''} 记录详情`} onCancel={() => setDetail(null)} onConfirm={() => setDetail(null)}>{detail && <div className="detail-content"><p>{detail.description}</p><dl><div><dt>证据</dt><dd>{detail.evidence || '-'}</dd></div><div><dt>当前版本</dt><dd>v{detail.version}</dd></div>{isPrintRun && <><div><dt>计划份数</dt><dd>{detail.plannedCopies ?? 0} 份</dd></div><div><dt>累计放行</dt><dd>{detail.releasedCopies ?? 0} 份</dd></div><div><dt>剩余数量</dt><dd>{detail.remainingCopies ?? 0} 份</dd></div></>}</dl><ColorTable records={[detail]} title="记录色彩读数" />{isPrintRun && detail.releases?.length ? <div className="revision-list"><h3>分批放行记录（{detail.releases.length} 笔，区间互不重叠）</h3>{detail.releases.map((entry) => <article key={entry.id}><strong>第 {entry.startSequence}–{entry.endSequence} 份 · {entry.completedCopies} 份</strong><span>{entry.pressCode} · {entry.actor} · {entry.reason}</span><code>{entry.requestId} · {formatDate(entry.createdAt)}</code></article>)}</div> : null}{detail.revisions?.length ? <div className="revision-list"><h3>版本链</h3>{detail.revisions.map((revision) => <article key={revision.id}><strong>v{revision.version} · {revision.status}</strong><span>{revision.actor} · {revision.reason}</span><code>{revision.requestId}</code></article>)}</div> : null}</div>}</ConfirmDialog>
    <PartialReleaseDialog open={Boolean(releaseTarget)} run={releaseTarget} onClose={() => setReleaseTarget(null)} onSubmitted={refreshDetail} onRefresh={(updated) => setReleaseTarget(updated)} />
  </main>;
}
