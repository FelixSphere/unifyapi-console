/*
UNIFYAPI-FORK: tests for the settlement screen's logic.

These decide what a period is, and what a row's state is. Two failures cost
real money: a billing period whose boundary is off by a day (so a month of
usage is billed twice or not at all), and a settled row that quietly stops
reflecting the basis it was settled on.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { SettlementRecord, SettlementRow, Statement } from '../../types'
import {
  DRIFT_TOLERANCE_USD,
  PRIMARY_ACTION_LABELS,
  SETTLEMENT_STATE_LABELS,
  VARIANCE_TOLERANCE_PCT,
  VARIANCE_VERDICT_LABELS,
  csvHref,
  customerInvoiceUI,
  deriveStatement,
  formatSigned,
  isPeriodClosed,
  monthPeriod,
  periodFromMonthLabel,
  recentPeriods,
  settlementState,
  statementIsBalanced,
  varianceVerdict,
} from '../settlement-logic'

function statement(over: Partial<Statement> = {}): Statement {
  return {
    kind: 'customer',
    counterparty: '7',
    label: 'acme',
    group: 'vip',
    period_start: '2026-08-01',
    period_end: '2026-08-31',
    lines: [],
    requests: 10,
    prompt_tokens: 1_000_000,
    cached_tokens: 0,
    completion_tokens: 100_000,
    amount_usd: 120,
    unpriced_requests: 0,
    ...over,
  }
}

function record(over: Partial<SettlementRecord> = {}): SettlementRecord {
  return {
    id: 1,
    kind: 'vendor',
    counterparty: 'anthropic',
    label: 'anthropic',
    period_start: '2026-08-01',
    period_end: '2026-08-31',
    amount_usd: 1000,
    invoiced_usd: 0,
    invoice_recorded: false,
    status: 'issued',
    note: '',
    statement_json: '{}',
    pricing_snapshot_date: '2026-08-30',
    created_at: 0,
    updated_at: 0,
    revision: 1,
    ...over,
  }
}

describe('monthPeriod', () => {
  test('last month is the one you can actually invoice', () => {
    // Mid-September: the billable month is August, whole.
    assert.deepEqual(monthPeriod(new Date(2026, 8, 15), 1), {
      start: '2026-08-01',
      end: '2026-08-31',
      label: '2026-08',
    })
  })

  test('a 30-day month ends on the 30th', () => {
    assert.equal(monthPeriod(new Date(2026, 9, 5), 1).end, '2026-09-30')
  })

  test('February is not assumed to be 30 days', () => {
    assert.equal(monthPeriod(new Date(2026, 2, 3), 1).end, '2026-02-28')
    assert.equal(monthPeriod(new Date(2028, 2, 3), 1).end, '2028-02-29')
  })

  test('walks back across a year boundary', () => {
    assert.deepEqual(monthPeriod(new Date(2026, 0, 9), 1), {
      start: '2025-12-01',
      end: '2025-12-31',
      label: '2025-12',
    })
  })

  test('resolves in local time, so an evening does not roll the month', () => {
    // 23:30 on the 1st. toISOString() would report the 2nd east of Greenwich —
    // harmless here, fatal on the boundary days this function is made of.
    assert.equal(
      monthPeriod(new Date(2026, 8, 1, 23, 30), 0).start,
      '2026-09-01'
    )
  })

  test('periods do not overlap or leave a gap', () => {
    const periods = recentPeriods(new Date(2026, 8, 15))
      .map((p) => [p.start, p.end])
      .sort()
    for (let i = 1; i < periods.length; i++) {
      const previousEnd = new Date(`${periods[i - 1][1]}T00:00:00`)
      const thisStart = new Date(`${periods[i][0]}T00:00:00`)
      const gapDays = (thisStart.getTime() - previousEnd.getTime()) / 86_400_000
      assert.equal(
        gapDays,
        1,
        `${periods[i - 1][1]} → ${periods[i][0]} must be consecutive`
      )
    }
  })
})

describe('periodFromMonthLabel', () => {
  test('supports an arbitrary natural month, not only the quick choices', () => {
    assert.deepEqual(periodFromMonthLabel('2024-02'), {
      start: '2024-02-01',
      end: '2024-02-29',
      label: '2024-02',
    })
  })

  test('keeps the current and previous five months one click away', () => {
    assert.deepEqual(
      recentPeriods(new Date(2026, 8, 15)).map((period) => period.label),
      ['2026-09', '2026-08', '2026-07', '2026-06', '2026-05', '2026-04']
    )
  })

  test('rejects malformed and impossible month labels', () => {
    assert.equal(periodFromMonthLabel('2026-13'), null)
    assert.equal(periodFromMonthLabel('June 2026'), null)
  })
})

describe('isPeriodClosed', () => {
  test('the current month is still accruing', () => {
    const today = new Date(2026, 8, 15)
    assert.equal(isPeriodClosed(monthPeriod(today, 0), today), false)
    assert.equal(isPeriodClosed(monthPeriod(today, 1), today), true)
  })

  test('the last day of a month is not closed until it is over', () => {
    // Billing August on August 31st would drop that day's usage.
    const today = new Date(2026, 7, 31)
    assert.equal(isPeriodClosed(monthPeriod(today, 0), today), false)
  })
})

describe('settlementState', () => {
  test('no record means nothing has been issued', () => {
    assert.equal(settlementState({ statement: statement() }), 'not-issued')
  })

  test('issued and settled are distinct', () => {
    assert.equal(
      settlementState({ statement: statement(), settlement: record() }),
      'issued'
    )
    assert.equal(
      settlementState({
        statement: statement(),
        settlement: record({ status: 'settled' }),
      }),
      'settled'
    )
  })

  test('drift outranks status — a settled row whose basis moved must say so', () => {
    // This is the case the screen exists to catch: August was paid against
    // $1000, and re-modelling August today gives $1150 because a purchasing
    // ratio changed in the meantime.
    const row: SettlementRow = {
      statement: statement({ amount_usd: 1150 }),
      settlement: record({ status: 'settled', amount_usd: 1000 }),
      drift_usd: 150,
    }
    assert.equal(settlementState(row), 'drifted')
  })

  test('float noise is not drift', () => {
    const row: SettlementRow = {
      statement: statement(),
      settlement: record(),
      drift_usd: DRIFT_TOLERANCE_USD / 2,
    }
    assert.equal(settlementState(row), 'issued')
  })

  test('void stays void even when the basis moved', () => {
    const row: SettlementRow = {
      statement: statement(),
      settlement: record({ status: 'void' }),
      drift_usd: 500,
    }
    assert.equal(settlementState(row), 'void')
  })
})

describe('customerInvoiceUI', () => {
  test('makes issuing the invoice the explicit first step', () => {
    const ui = customerInvoiceUI('not-issued')
    assert.equal(ui.heading, 'Create the customer invoice')
    assert.equal(ui.saveLabel, 'Issue invoice')
    assert.equal(ui.canOpen, false)
    assert.match(ui.description, /CSV is supporting line-item detail/)
  })

  test('makes the PDF the next action after issue', () => {
    const ui = customerInvoiceUI('issued')
    assert.equal(ui.heading, 'Invoice ready to send')
    assert.equal(ui.canOpen, true)
    assert.match(ui.description, /save it as a PDF/)
  })

  test('does not offer a void invoice for sending', () => {
    const ui = customerInvoiceUI('void')
    assert.equal(ui.heading, 'Invoice voided')
    assert.equal(ui.canOpen, false)
  })
})

describe('varianceVerdict', () => {
  test('no invoice yet is pending, not a variance', () => {
    // Without this, every vendor whose invoice has not arrived reads as a 100%
    // discrepancy, every month, until nobody reads the column.
    assert.equal(
      varianceVerdict({ statement: statement(), settlement: record() }),
      'pending'
    )
  })

  test('inside the tolerance is reconciled', () => {
    const inside = record({
      invoice_recorded: true,
      amount_usd: 1000,
      invoiced_usd: 1000 * (1 + (VARIANCE_TOLERANCE_PCT - 0.1) / 100),
    })
    assert.equal(
      varianceVerdict({ statement: statement(), settlement: inside }),
      'reconciled'
    )
  })

  test('the tolerance matches the server', () => {
    // service/reconcile.go uses 2.0.
    assert.equal(VARIANCE_TOLERANCE_PCT, 2.0)
  })

  test('over and under are distinguished, because they point at different bugs', () => {
    assert.equal(
      varianceVerdict({
        statement: statement(),
        settlement: record({
          invoice_recorded: true,
          amount_usd: 1000,
          invoiced_usd: 1200,
        }),
      }),
      'over'
    )
    assert.equal(
      varianceVerdict({
        statement: statement(),
        settlement: record({
          invoice_recorded: true,
          amount_usd: 1000,
          invoiced_usd: 800,
        }),
      }),
      'under'
    )
  })

  test('an invoice against nothing modelled is a finding, not a division by zero', () => {
    assert.equal(
      varianceVerdict({
        statement: statement(),
        settlement: record({
          invoice_recorded: true,
          amount_usd: 0,
          invoiced_usd: 40,
        }),
      }),
      'over'
    )
    assert.equal(
      varianceVerdict({
        statement: statement(),
        settlement: record({
          invoice_recorded: true,
          amount_usd: 0,
          invoiced_usd: 0,
        }),
      }),
      'reconciled'
    )
  })
})

describe('deriveStatement', () => {
  test('a customer statement is stated as read from the ledger', () => {
    const [usage] = deriveStatement(statement())
    assert.equal(usage.amountUSD, 120)
    assert.ok(usage.noteKey)
    assert.match(usage.noteKey, /quota actually deducted/)
  })

  test('never presents wallet top-ups as invoice receipts', () => {
    assert.equal(deriveStatement(statement()).length, 1)
  })

  test('a vendor statement is stated as modelled', () => {
    const [cost] = deriveStatement(
      statement({ kind: 'vendor', amount_usd: 1000 })
    )
    assert.ok(cost.noteKey)
    assert.match(cost.noteKey, /official price/)
  })

  test('an unpriceable vendor line warns against paying from it', () => {
    // Understated, not overstated: the risk on the vendor side is under-paying
    // an invoice, which is the opposite of the customer-side risk.
    const steps = deriveStatement(
      statement({
        kind: 'vendor',
        unpriced_requests: 12,
        unpriced_models: ['glm-5.3'],
      })
    )
    const caveat = steps.at(-1)
    assert.ok(caveat)
    assert.ok(caveat.noteKey)
    assert.match(caveat.noteKey, /understated/)
    assert.deepEqual(caveat.noteParams, { count: 12, models: 'glm-5.3' })
  })
})

describe('statementIsBalanced', () => {
  test('model and channel detail add up to the UI total', () => {
    assert.equal(
      statementIsBalanced(
        statement({
          amount_usd: 3,
          requests: 3,
          lines: [
            {
              model: 'gpt-4o',
              channel_id: 1,
              requests: 1,
              prompt_tokens: 10,
              cached_tokens: 0,
              completion_tokens: 2,
              amount_usd: 1,
            },
            {
              model: 'gpt-4o',
              channel_id: 2,
              requests: 2,
              prompt_tokens: 20,
              cached_tokens: 5,
              completion_tokens: 4,
              amount_usd: 2,
            },
          ],
        })
      ),
      true
    )
  })

  test('refuses a UI document whose summary disagrees with its details', () => {
    assert.equal(
      statementIsBalanced(
        statement({
          amount_usd: 4,
          requests: 3,
          lines: [
            {
              model: 'gpt-4o',
              requests: 3,
              prompt_tokens: 10,
              cached_tokens: 0,
              completion_tokens: 2,
              amount_usd: 3,
            },
          ],
        })
      ),
      false
    )
  })
})

describe('csvHref', () => {
  const period = { start: '2026-08-01', end: '2026-08-31', label: '2026-08' }

  test('a single counterparty', () => {
    const href = csvHref('customer', period, '7')
    assert.match(href, /kind=customer/)
    assert.match(href, /counterparty=7/)
  })

  test('the whole period omits the filter rather than sending an empty one', () => {
    assert.equal(csvHref('vendor', period).includes('counterparty'), false)
  })
})

describe('label records', () => {
  test('every state and verdict has a label', () => {
    // Exhaustive Records make this a type error too, but the runtime check
    // catches an entry left as an empty string.
    for (const label of [
      ...Object.values(SETTLEMENT_STATE_LABELS),
      ...Object.values(VARIANCE_VERDICT_LABELS),
      ...Object.values(PRIMARY_ACTION_LABELS),
    ]) {
      assert.ok(label.length > 0)
    }
  })

  test('the primary action distinguishes billing from paying', () => {
    // "Issue statement" is an act with an outward-facing document; "Record
    // settlement" is bookkeeping about money we owe. One button label for both
    // would misdescribe whichever side you are on.
    assert.notEqual(
      PRIMARY_ACTION_LABELS.customer,
      PRIMARY_ACTION_LABELS.vendor
    )
    assert.notEqual(
      PRIMARY_ACTION_LABELS.update,
      PRIMARY_ACTION_LABELS.customer
    )
  })
})

describe('formatSigned', () => {
  test('an over-invoice carries a plus', () => {
    assert.equal(formatSigned(120.5), '+$120.50')
    assert.equal(formatSigned(-120.5), '-$120.50')
  })
})
