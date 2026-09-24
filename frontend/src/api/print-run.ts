
import { request } from './client';
import type { DomainRecord, RunReleaseRecord } from '../types/domain';

export async function listPrintRun(page = 1, pageSize = 20, search = '') {
  return request<DomainRecord[]>(`/runs?page=${page}&pageSize=${pageSize}&search=${encodeURIComponent(search)}`);
}
export async function createPrintRun(input: Partial<DomainRecord>) {
  return request<DomainRecord>('/runs', { method: 'POST', body: JSON.stringify(input) });
}
export async function transitionPrintRun(id: number, status: string, expectedVersion: number, reason: string) {
  return request<DomainRecord>(`/runs/${id}/transition`, {
    method: 'POST', body: JSON.stringify({ status, expectedVersion, reason }),
  });
}

export interface PartialReleaseInput {
  expectedVersion: number;
  completedCopies: number;
  pressCode: string;
  reason?: string;
}

// 分批放行：本次完成数 + 印刷机台；满数后后端才把批次转为已放行。
export async function submitRunRelease(runId: number, input: PartialReleaseInput) {
  return request<DomainRecord>(`/runs/${runId}/releases`, { method: 'POST', body: JSON.stringify(input) });
}

export async function listRunReleases(runId: number): Promise<RunReleaseRecord[]> {
  const result = await request<DomainRecord>(`/runs/${runId}`);
  return result.data.releases || [];
}
