import { useAuthStore } from '@/stores/auth-store'

/*
Copyright (C) 2026 UnifyAI
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { fetchChannelVideoTest, submitChannelVideoTest } from '../api'
import type { ChannelTestResponse } from '../types'

// Keep the task ID when polling is interrupted: clicking Test again resumes
// this task instead of buying another generation. The server task is durable.
const pendingTasks = new Map<string, string>()

export async function runVideoChannelTest(
  id: number,
  model: string,
  optionsJSON = '',
  onProgress?: (message: string) => void,
  dependencies = {
    submit: submitChannelVideoTest,
    fetch: fetchChannelVideoTest,
    wait: () => new Promise<void>((resolve) => setTimeout(resolve, 4000)),
    maxPolls: 300,
  }
): Promise<ChannelTestResponse> {
  const key = `${useAuthStore.getState().auth.session?.sid || 'anonymous'}:${id}:${model}`
  let taskId = pendingTasks.get(key)
  if (!taskId) {
    const options: unknown = optionsJSON.trim() ? JSON.parse(optionsJSON) : {}
    if (!options || typeof options !== 'object' || Array.isArray(options)) {
      throw new Error('Video test options must be a JSON object')
    }
    const submitted = await dependencies.submit(id, { ...options, model })
    taskId = submitted.id || submitted.task_id
    if (!taskId) {
      throw new Error(
        submitted.error?.message ||
          submitted.message ||
          'Video task submission failed'
      )
    }
    pendingTasks.set(key, taskId)
  }
  const pending = (detail: string): ChannelTestResponse => ({
    success: false,
    error_code: 'video_test_pending',
    message: `${detail} Task: ${taskId}. Click Test again to resume checking this task.`,
  })
  onProgress?.(`Video task ${taskId}: queued`)
  for (let poll = 0; poll < dependencies.maxPolls; poll++) {
    await dependencies.wait()
    let result
    try {
      result = await dependencies.fetch(id, taskId)
    } catch {
      return pending(
        'Status check interrupted; generation may still be running.'
      )
    }
    if (!result.success)
      return pending(result.message || 'Unable to read video task status.')
    if (result.status === 'completed' || result.status === 'failed') {
      pendingTasks.delete(key)
      return {
        success: result.status === 'completed',
        message: result.message,
        time: result.time,
      }
    }
    onProgress?.(result.message || `Video task ${taskId}: ${result.status}`)
  }
  return pending('Generation is still pending.')
}
