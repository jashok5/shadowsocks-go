export interface UserOverview {
  user_id: number
  upload: number
  download: number
  online_ip_count: number
  online_ips: string[]
  last_seen_unix: number
  detect_count: number
  ports: number[]
}

export interface SSStat {
  Port: number
  ActiveTCP: number
  TCPDrop: number
  UDPDrop: number
  UDPAssocRunner: {
    ActiveReaders: number
    Packets: number
    Errors: number
  }
  UDPAssocAlerts: {
    ErrorDelta: number
    Packets: number
    Errors: number
    Warn: boolean
  }
  UDPSessionCache: {
    Size: number
    Hits: number
    Misses: number
    Creates: number
    Evicted: number
    Expired: number
    Sweeps: number
  }
  UDPResolveCache: {
    Size: number
    Hits: number
    Misses: number
    Creates: number
    Evicted: number
    Expired: number
    Sweeps: number
  }
}

export interface SSRStat {
  Port: number
  UDPAssocRunner: {
    ActiveReaders: number
    Packets: number
    Errors: number
  }
  UDPAssocAlerts: {
    ErrorDelta: number
    Packets: number
    Errors: number
    Warn: boolean
  }
  UDPAssocCache: {
    Size: number
    Hits: number
    Misses: number
    Creates: number
    Evicted: number
    Expired: number
    Sweeps: number
  }
  UDPResolveCache: {
    Size: number
    Hits: number
    Misses: number
    Creates: number
    Evicted: number
    Expired: number
    Sweeps: number
  }
  UserOnlineCount: Record<string, number>
}

export interface ATPStat {
  listen: string
  port: number
  transport: string
  sni: string
  users: number
  rules: number
  proxy_active: boolean
  proxy_generation: number
  cert_not_after_unix: number
  cert_remaining_sec: number
  last_error?: string
  last_apply_unix: number
}

export interface OverviewResponse {
  now_unix: number
  version: string
  go_version: string
  driver: string
  started_at_unix: number
  uptime_seconds: number
  ports: number
  users: number
  online_users: number
  online_ips: number
  total_upload: number
  total_download: number
  wrong_ips: number
  user_list: UserOverview[]
  mem: {
    goroutines: number
    heap_alloc: number
    heap_inuse: number
    heap_objects: number
    num_gc: number
    rss_bytes: number
  }
  ss_stats?: SSStat[]
  ssr_stats?: SSRStat[]
  atp_stats?: ATPStat
}

export interface UserDetailResponse {
  user_id: number
  upload: number
  download: number
  online_ip_count: number
  online_ips: string[]
  detect_rules: number[]
  ports: number[]
  active: boolean
}
