
import { request } from './client';
import type { DomainRecord, RunReleaseRecord, RunReleaseResult } from '../types/domain';

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
export async function listRunReleases(id: number) {
  return request<RunReleaseRecord[]>(`/runs/${id}/releases`);
}
export async function createRunRelease(id: number, input: { quantity: number; pressUnit: string; note?: string }) {
  return request<RunReleaseResult>(`/runs/${id}/releases`, { method: 'POST', body: JSON.stringify(input) });
}
