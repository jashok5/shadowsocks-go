import { http } from './http'
import type { LogsSnapshotResponse } from '../types/logs'

export async function fetchLogsSnapshot(limit = 300): Promise<LogsSnapshotResponse> {
  const { data } = await http.get<LogsSnapshotResponse>('/logs', {
    params: { limit },
  })
  return data
}
