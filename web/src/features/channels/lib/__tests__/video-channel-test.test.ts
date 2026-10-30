/*
Copyright (C) 2026 UnifyAI
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { describe, expect, test } from 'bun:test'

import { runVideoChannelTest } from '../video-channel-test'

describe('video channel generation tests', () => {
  test('waits for completion instead of accepting submission', async () => {
    const requests: Record<string, unknown>[] = []
    let polls = 0
    const result = await runVideoChannelTest(
      10001,
      'MiniMax-H3',
      '{"model":"wrong","images":["https://example.com/a.png"]}',
      undefined,
      {
        submit: async (_id, request) => {
          requests.push(request)
          return { id: 'test-success' }
        },
        fetch: async () => ({
          success: true,
          status: ++polls < 3 ? 'in_progress' : 'completed',
          time: 12,
        }),
        wait: async () => {},
        maxPolls: 4,
      }
    )
    expect(requests).toEqual([
      { model: 'MiniMax-H3', images: ['https://example.com/a.png'] },
    ])
    expect(polls).toBe(3)
    expect(result).toMatchObject({ success: true, time: 12 })
  })
  test('reports generation failure', async () => {
    const result = await runVideoChannelTest(
      10002,
      'MiniMax-H3',
      '',
      undefined,
      {
        submit: async () => ({ id: 'test-failure' }),
        fetch: async () => ({
          success: true,
          status: 'failed',
          message: 'provider rejected input',
        }),
        wait: async () => {},
        maxPolls: 1,
      }
    )
    expect(result).toMatchObject({
      success: false,
      message: 'provider rejected input',
    })
  })
  test('interrupted polling resumes without buying another generation', async () => {
    let submits = 0
    const dependencies = {
      submit: async () => {
        submits++
        return { id: 'test-resume' }
      },
      fetch: async () => {
        throw new Error('network interrupted')
      },
      wait: async () => {},
      maxPolls: 1,
    }
    const pending = await runVideoChannelTest(
      10003,
      'MiniMax-H3',
      '',
      undefined,
      dependencies
    )
    expect(pending.error_code).toBe('video_test_pending')
    expect(pending.message).toContain('test-resume')
    const complete = await runVideoChannelTest(
      10003,
      'MiniMax-H3',
      '',
      undefined,
      {
        ...dependencies,
        fetch: async () => ({ success: true, status: 'completed' }),
      }
    )
    expect(complete.success).toBe(true)
    expect(submits).toBe(1)
  })
  test('rejects invalid options and submission errors before polling', async () => {
    const dependencies = {
      submit: async () => ({ message: 'Insufficient quota' }),
      fetch: async () => {
        throw new Error('should not poll')
      },
      wait: async () => {},
      maxPolls: 1,
    }
    await expect(
      runVideoChannelTest(10004, 'MiniMax-H3', '[]', undefined, dependencies)
    ).rejects.toThrow('JSON object')
    await expect(
      runVideoChannelTest(10004, 'MiniMax-H3', '', undefined, dependencies)
    ).rejects.toThrow('Insufficient quota')
  })
})
