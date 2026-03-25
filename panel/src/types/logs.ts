export interface LogEntry {
  id: number
  timestamp: number
  text: string
}

export interface LogsSnapshotResponse {
  latest_id: number
  items: LogEntry[]
}
