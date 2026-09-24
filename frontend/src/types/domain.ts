
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
  createdAt: string;
  updatedAt: string;
  // 印刷批次分批放行字段：计划份数在建批时记录，累计/剩余由后端放行台账实时汇总。
  plannedCopies?: number;
  releasedCopies?: number;
  remainingCopies?: number;
  releases?: RunReleaseRecord[];
  revisions?: RevisionRecord[];
}

export interface RunReleaseRecord {
  id: number;
  printRunId: number;
  startSequence: number;
  endSequence: number;
  completedCopies: number;
  pressCode: string;
  actor: string;
  requestId: string;
  reason: string;
  createdAt: string;
}

export interface RevisionRecord {
  id: number; version: number; status: string; name: string; metricValue: number;
  metricUnit: string; evidence: string; actor: string; requestId: string; reason: string; createdAt: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
