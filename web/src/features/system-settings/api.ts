/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'

import type {
  ConfirmPaymentComplianceResponse,
  FetchUpstreamRatiosRequest,
  LogCleanupTask,
  SystemOptionsResponse,
  SystemTaskListResponse,
  SystemTaskResponse,
  UpdateOptionRequest,
  UpdateOptionResponse,
  ChannelCostResponse,
  PricingBaselineResponse,
  UpdateChannelCostResponse,
  UpdatePricingDiscountResponse,
  UpstreamChannelsResponse,
  UpstreamRatiosResponse,
} from './types'

export async function getSystemOptions() {
  const res = await api.get<SystemOptionsResponse>('/api/option/')
  return res.data
}

export async function updateSystemOption(request: UpdateOptionRequest) {
  const res = await api.put<UpdateOptionResponse>('/api/option/', request)
  return res.data
}

export async function confirmPaymentCompliance() {
  const res = await api.post<ConfirmPaymentComplianceResponse>(
    '/api/option/payment_compliance',
    { confirmed: true }
  )
  return res.data
}

export async function startLogCleanupTask(targetTimestamp: number) {
  const res = await api.post<SystemTaskResponse<LogCleanupTask>>(
    '/api/system-task/log-cleanup',
    null,
    {
      params: { target_timestamp: targetTimestamp },
    }
  )
  return res.data
}

export async function getCurrentLogCleanupTask() {
  const res = await api.get<SystemTaskResponse<LogCleanupTask | null>>(
    '/api/system-task/current',
    {
      params: { type: 'log_cleanup' },
    }
  )
  return res.data
}

export async function getSystemTask(taskId: string) {
  const res = await api.get<SystemTaskResponse<LogCleanupTask>>(
    `/api/system-task/${taskId}`
  )
  return res.data
}

export async function listSystemTasks(limit = 20) {
  const res = await api.get<SystemTaskListResponse>('/api/system-task/list', {
    params: { limit },
  })
  return res.data
}

export async function resetModelRatios() {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/rest_model_ratio'
  )
  return res.data
}

export async function getUpstreamChannels() {
  const res = await api.get<UpstreamChannelsResponse>(
    '/api/ratio_sync/channels'
  )
  return res.data
}

export async function fetchUpstreamRatios(request: FetchUpstreamRatiosRequest) {
  const res = await api.post<UpstreamRatiosResponse>(
    '/api/ratio_sync/fetch',
    request
  )
  return res.data
}

/*
 * UNIFYAPI-FORK: the pricing baseline and the per-model customer discount.
 *
 * These are separate from the ratio endpoints above on purpose. Saving a ratio
 * writes an options row that replaces the whole code baseline; saving a
 * discount leaves the official prices alone and only records the multiplier we
 * sell at. See setting/ratio_setting/unifyapi_discount.go.
 */
export async function getPricingBaseline() {
  const res = await api.get<PricingBaselineResponse>('/api/pricing/baseline')
  return res.data
}

export async function updatePricingDiscount(discounts: Record<string, number>) {
  const res = await api.put<UpdatePricingDiscountResponse>(
    '/api/pricing/discount',
    { discounts }
  )
  return res.data
}

/*
 * UNIFYAPI-FORK: per-channel upstream cost, for reconciliation only.
 *
 * Kept separate from the discount endpoints because it is the one pricing number
 * that must NOT reach a customer's invoice: routing is load balanced, so a
 * channel's cost varies by route while a customer's price must not.
 */
export async function getChannelCost() {
  const res = await api.get<ChannelCostResponse>('/api/pricing/channel_cost')
  return res.data
}

export async function updateChannelCost(costRatios: Record<string, number>) {
  const res = await api.put<UpdateChannelCostResponse>(
    '/api/pricing/channel_cost',
    { cost_ratios: costRatios }
  )
  return res.data
}
