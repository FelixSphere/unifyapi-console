import type {
  CustomerModelCatalogEntry,
  CustomerModelChannel,
  CustomerModelContract,
} from '../types'

export const CONTRACT_PROBLEM_LABELS: Record<string, string> = {
  tenant: 'Select a customer company',
  model: 'Select a model',
  discount: 'Discount multiplier must be greater than 0 and at most 10',
  duplicateChannel: 'A channel can only be selected once',
  missingChannel: 'An enabled contract needs at least one dedicated channel',
} as const

export function eligibleContractChannels(
  channels: CustomerModelChannel[],
  model: string,
  currentContractId: number
): CustomerModelChannel[] {
  return channels.filter(
    (channel) =>
      channel.status === 1 &&
      channel.single_model &&
      channel.models.length === 1 &&
      channel.models[0] === model &&
      (channel.bound_contract_id === 0 ||
        channel.bound_contract_id === currentContractId)
  )
}

export function contractPrice(
  entry: CustomerModelCatalogEntry | undefined,
  discount: number
): { input: number; output: number } | null {
  if (!entry || !Number.isFinite(discount) || discount <= 0) return null
  return {
    input: entry.official_input_usd * discount,
    output: entry.official_output_usd * discount,
  }
}

export function contractFor(
  contracts: CustomerModelContract[],
  tenantId: number,
  model: string
): CustomerModelContract | undefined {
  return contracts.find(
    (contract) => contract.tenant_id === tenantId && contract.model === model
  )
}

export function validateContractDraft(input: {
  tenantId: number
  model: string
  discount: number
  channelIds: number[]
  enabled: boolean
}): string[] {
  const problems: string[] = []
  if (input.tenantId <= 0) problems.push(CONTRACT_PROBLEM_LABELS.tenant)
  if (!input.model) problems.push(CONTRACT_PROBLEM_LABELS.model)
  if (
    !Number.isFinite(input.discount) ||
    input.discount <= 0 ||
    input.discount > 10
  ) {
    problems.push(CONTRACT_PROBLEM_LABELS.discount)
  }
  if (new Set(input.channelIds).size !== input.channelIds.length) {
    problems.push(CONTRACT_PROBLEM_LABELS.duplicateChannel)
  }
  if (input.enabled && input.channelIds.length === 0) {
    problems.push(CONTRACT_PROBLEM_LABELS.missingChannel)
  }
  return problems
}

export function formatContractUSD(value: number): string {
  if (value < 0.1) return `$${value.toFixed(4)}`
  return `$${value.toFixed(2)}`
}
