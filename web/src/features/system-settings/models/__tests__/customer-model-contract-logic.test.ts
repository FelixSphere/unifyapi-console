import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import type { CustomerModelChannel } from '../../types'
import {
  contractPrice,
  eligibleContractChannels,
  validateContractDraft,
} from '../customer-model-contract-logic'

const channels: CustomerModelChannel[] = [
  {
    id: 1,
    name: 'Acme Opus',
    status: 1,
    models: ['claude-opus-5'],
    group: 'default',
    priority: 0,
    weight: 0,
    single_model: true,
    bound_contract_id: 0,
  },
  {
    id: 2,
    name: 'Shared Anthropic',
    status: 1,
    models: ['claude-opus-5', 'claude-fable-5.1'],
    group: 'default',
    priority: 0,
    weight: 0,
    single_model: false,
    bound_contract_id: 0,
  },
  {
    id: 3,
    name: 'Disabled Opus',
    status: 2,
    models: ['claude-opus-5'],
    group: 'default',
    priority: 0,
    weight: 0,
    single_model: true,
    bound_contract_id: 0,
  },
]

describe('customer model contract logic', () => {
  it('only offers enabled channels containing exactly the selected model', () => {
    assert.deepEqual(
      eligibleContractChannels(channels, 'claude-opus-5', 0).map(
        (channel) => channel.id
      ),
      [1]
    )
  })

  it('never offers a channel owned by another company-model contract', () => {
    const owned = channels.map((channel) =>
      channel.id === 1 ? { ...channel, bound_contract_id: 17 } : channel
    )
    assert.deepEqual(
      eligibleContractChannels(owned, 'claude-opus-5', 0).map(
        (channel) => channel.id
      ),
      []
    )
    assert.deepEqual(
      eligibleContractChannels(owned, 'claude-opus-5', 17).map(
        (channel) => channel.id
      ),
      [1]
    )
  })

  it('shows official price times the customer-model multiplier', () => {
    assert.deepEqual(
      contractPrice(
        {
          model: 'claude-opus-5',
          vendor: 'anthropic',
          official_input_usd: 5,
          official_output_usd: 25,
        },
        0.7
      ),
      { input: 3.5, output: 17.5 }
    )
  })

  it('refuses an enabled contract without a dedicated channel', () => {
    assert.deepEqual(
      validateContractDraft({
        tenantId: 4,
        model: 'claude-opus-5',
        discount: 0.7,
        channelIds: [],
        enabled: true,
      }),
      ['An enabled contract needs at least one dedicated channel']
    )
  })
})
