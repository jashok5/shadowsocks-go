import { http } from './http'
import type { OverviewResponse, UserDetailResponse } from '../types/panel'

export async function verifyToken(): Promise<boolean> {
  await http.get('/auth/verify')
  return true
}

export async function fetchOverview(): Promise<OverviewResponse> {
  const { data } = await http.get<OverviewResponse>('/overview')
  return data
}

export async function fetchUserDetail(userID: number): Promise<UserDetailResponse> {
  const { data } = await http.get<UserDetailResponse>(`/users/${userID}`)
  return data
}
