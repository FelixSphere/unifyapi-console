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
import { downloadAuthenticatedFile } from '@/lib/authenticated-download'
import { printAuthenticatedDocument } from '@/lib/print-authenticated-document'

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
  ReconcileParams,
  ReconcileResponse,
  ReconcileSnapshotsResponse,
  ExtraModelsResponse,
  ModelPriceLookupResponse,
  UpdateExtraModelsResponse,
  IssueSettlementParams,
  SettlementRecord,
  SettlementResponse,
  SettlementStatus,
  StatementKind,
  UpdateChannelCostResponse,
  UpdatePricingDiscountResponse,
  GroupModelPricingResponse,
  UpdateGroupModelPricingResponse,
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

export async function getGroupModelPricing() {
  const res = await api.get<GroupModelPricingResponse>(
    '/api/pricing/group_model'
  )
  return res.data
}

export async function updateGroupModelPricing(
  group: string,
  discounts: Record<string, number>
) {
  const res = await api.put<UpdateGroupModelPricingResponse>(
    '/api/pricing/group_model',
    { group, discounts }
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

/*
 * UNIFYAPI-FORK: the profit view.
 *
 * Revenue comes from the consume-log ledger untouched; cost is modelled from
 * tokens x the vendor's official price x the channel's purchasing ratio. The
 * two sides are built differently on purpose -- see service/reconcile.go.
 */
export async function getReconciliation(params: ReconcileParams) {
  const res = await api.get<ReconcileResponse>('/api/pricing/reconcile', {
    params,
  })
  return res.data
}

export async function getReconcileSnapshots(groupBy?: string, limit = 30) {
  const res = await api.get<ReconcileSnapshotsResponse>(
    '/api/pricing/reconcile/snapshots',
    { params: { group_by: groupBy, limit } }
  )
  return res.data
}

export async function runReconcileNow(params: {
  start?: string
  end?: string
  force?: boolean
}) {
  const res = await api.post<{ success: boolean; message?: string }>(
    '/api/pricing/reconcile/run',
    null,
    { params }
  )
  return res.data
}

/*
 * UNIFYAPI-FORK: settlement.
 *
 * Note what is NOT sent when issuing: our own amount. The server always
 * recomputes it from the consume log, so the screen cannot be talked into
 * agreeing with a counterparty's invoice. See controller/settlement.go.
 */
export async function getSettlements(params: {
  kind: StatementKind
  start: string
  end: string
}) {
  const res = await api.get<SettlementResponse>('/api/pricing/settlement', {
    params,
  })
  return res.data
}

export async function issueSettlement(params: IssueSettlementParams) {
  const res = await api.post<{
    success: boolean
    message?: string
    data?: SettlementRecord
  }>('/api/pricing/settlement', params)
  return res.data
}

export async function updateSettlement(
  id: number,
  body: {
    invoiced_usd: number
    invoice_recorded: boolean
    status: SettlementStatus
    note: string
  }
) {
  const res = await api.put<{ success: boolean; message?: string }>(
    `/api/pricing/settlement/${id}`,
    body
  )
  return res.data
}

export async function deleteSettlement(id: number) {
  const res = await api.delete<{ success: boolean; message?: string }>(
    `/api/pricing/settlement/${id}`
  )
  return res.data
}

export async function downloadSettlementCSV(path: string) {
  return downloadAuthenticatedFile(path)
}

export async function downloadFrozenSettlementCSV(settlementId: number) {
  return downloadAuthenticatedFile(
    `/api/pricing/settlement/${settlementId}/statement.csv`
  )
}

export async function downloadReconciliationCSV(path: string) {
  return downloadAuthenticatedFile(path)
}

export async function printCustomerInvoice(settlementId: number) {
  return printAuthenticatedDocument(
    `/api/pricing/settlement/${settlementId}/invoice`
  )
}

/*
 * UNIFYAPI-FORK: admin-added model pricing.
 *
 * The whole table is sent on save, because that is what the underlying option
 * row is. Sending a single model would be indistinguishable from sending a
 * table containing one model, and the difference between those two readings is
 * every other model's price.
 */
export async function getExtraModels() {
  const res = await api.get<ExtraModelsResponse>('/api/pricing/extra_models')
  return res.data
}

export async function updateExtraModels(
  models: Record<string, Record<string, number | string>>
) {
  const res = await api.put<UpdateExtraModelsResponse>(
    '/api/pricing/extra_models',
    {
      models,
    }
  )
  return res.data
}

/**
 * lookupModelPrice asks models.dev what a model costs.
 *
 * Returns every provider's listing rather than one price: the same id is sold by
 * hundreds of providers at different rates, and the server picking one would
 * make the console invent a commercial decision. The vendor's own listing sorts
 * first; the operator chooses.
 */
export async function lookupModelPrice(model: string) {
  const res = await api.get<ModelPriceLookupResponse>(
    '/api/pricing/extra_models/lookup',
    { params: { model } }
  )
  return res.data
}
