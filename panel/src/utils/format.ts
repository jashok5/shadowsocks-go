export function formatBytes(value: number): string {
  const abs = Math.abs(value)
  if (abs < 1024) return `${value} B`
  if (abs < 1024 ** 2) return `${(value / 1024).toFixed(2)} KiB`
  if (abs < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(2)} MiB`
  if (abs < 1024 ** 4) return `${(value / 1024 ** 3).toFixed(2)} GiB`
  return `${(value / 1024 ** 4).toFixed(2)} TiB`
}

export function formatNumber(value: number): string {
  return new Intl.NumberFormat('zh-CN').format(value)
}

export function formatDuration(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds))
  const day = Math.floor(safe / 86400)
  const hour = Math.floor((safe % 86400) / 3600)
  const minute = Math.floor((safe % 3600) / 60)
  const second = safe % 60
  if (day > 0) return `${day}d ${hour}h ${minute}m`
  if (hour > 0) return `${hour}h ${minute}m ${second}s`
  if (minute > 0) return `${minute}m ${second}s`
  return `${second}s`
}

export function formatDateTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString('zh-CN')
}
